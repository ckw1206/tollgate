package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	s.ChatAs(w, r, authFrom(r.Context()))
}

// ChatAs runs the chat path for an authenticated principal: a key, or a
// key-less one (playground session) whose limits and model access come from
// the user's group only.
func (s *Server) ChatAs(w http.ResponseWriter, r *http.Request, info *repo.AuthInfo) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		WriteError(w, 413, "invalid_request_error", "request_too_large", "request body too large")
		return
	}
	req, err := llm.ParseChatRequest(body)
	if err != nil {
		WriteError(w, 400, "invalid_request_error", "invalid_request", err.Error())
		return
	}
	m, p, ok := s.Registry.Get(req.Model)
	if !ok {
		WriteError(w, 404, "invalid_request_error", "model_not_found", fmt.Sprintf("model %q not found", req.Model))
		return
	}
	if !modelAllowed(info, m.Name) {
		WriteError(w, 403, "permission_error", "model_not_allowed", fmt.Sprintf("model %q isn't allowed for this key, its owner or the owner's group", m.Name))
		return
	}
	if !s.admit(w, r, info) {
		return
	}

	start := s.Now()
	rec := newRecord(info, m, start)
	upCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), upstreamTimeout)
	defer cancel()

	var u llm.Usage
	if req.Stream {
		u, err = s.stream(w, r, upCtx, m, p, req)
	} else {
		var res *llm.Result
		if res, err = p.Chat(upCtx, m, req); err == nil {
			u = res.Usage
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Tollgate-Cost", llm.FormatUSD(llm.Cost(u, m.Prices)))
			w.WriteHeader(200)
			w.Write(res.Body)
		}
	}
	if errors.Is(err, llm.ErrBadRequest) {
		if !req.Stream || !streamStarted(w) {
			WriteError(w, 400, "invalid_request_error", "unsupported", err.Error())
		}
		return // never reached upstream; nothing to bill
	}
	fill(&rec, u, m, s.Now().Sub(start))
	if err != nil {
		rec.Status, rec.ErrorMessage = "error", err.Error()
		rec.HTTPStatus = 200 // headers already sent for streams
		if !req.Stream || !streamStarted(w) {
			rec.HTTPStatus = writeUpstreamError(w, err)
		}
	}
	s.finish(info, rec, u)
}

// stream proxies SSE. If the client goes away, it stops writing but lets the
// provider drain upstream (upCtx is detached from the client) so usage is exact.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, upCtx context.Context, m *llm.Model, p llm.Provider, req *llm.ChatRequest) (llm.Usage, error) {
	sw := &streamWriter{w: w}
	u, err := p.ChatStream(upCtx, m, req, sw.write)
	if sw.gone {
		return u, errors.New("client disconnected mid-stream")
	}
	if err != nil {
		if sw.started {
			b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": "api_error", "code": "upstream_error"}})
			sw.write(b)
		}
		return u, err
	}
	sw.start()
	io.WriteString(w, "data: [DONE]\n\n")
	sw.flush()
	return u, nil
}

type streamWriter struct {
	w             http.ResponseWriter
	started, gone bool
}

func (sw *streamWriter) start() {
	if sw.started {
		return
	}
	sw.started = true
	h := sw.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	sw.w.WriteHeader(200)
}

func (sw *streamWriter) flush() {
	if f, ok := sw.w.(http.Flusher); ok {
		f.Flush()
	}
}

// write never returns an error: after a client disconnect it silently discards,
// so the provider keeps reading upstream to the end.
func (sw *streamWriter) write(chunk []byte) error {
	if sw.gone {
		return nil
	}
	sw.start()
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", chunk); err != nil {
		sw.gone = true
		return nil
	}
	sw.flush()
	return nil
}

// streamStarted reports whether headers went out (so a JSON error can't be written).
func streamStarted(w http.ResponseWriter) bool {
	return w.Header().Get("Content-Type") == "text/event-stream"
}

func writeUpstreamError(w http.ResponseWriter, err error) int {
	var ue *llm.UpstreamError
	if errors.As(err, &ue) {
		status := ue.Status
		if status < 400 || status > 599 {
			status = 502
		}
		WriteError(w, status, "api_error", "upstream_error", ue.Message)
		return status
	}
	WriteError(w, 502, "api_error", "upstream_error", err.Error())
	return 502
}

func newRecord(info *repo.AuthInfo, m *llm.Model, start time.Time) repo.SpendRecord {
	source := "api"
	if info.Key.ID == "" { // key-less (playground) principals bill the group only
		source = "playground"
	}
	return repo.SpendRecord{KeyID: info.Key.ID, Source: source, UserID: info.Key.UserID, GroupID: info.Key.GroupID,
		ModelID: m.ID, ModelName: m.Name, Provider: m.Provider, Status: "success", HTTPStatus: 200, CreatedAt: start}
}

func fill(rec *repo.SpendRecord, u llm.Usage, m *llm.Model, latency time.Duration) {
	rec.InputTokens, rec.OutputTokens, rec.CacheReadTokens, rec.CacheWriteTokens = u.Input, u.Output, u.CacheRead, u.CacheWrite
	rec.CostNanos = llm.Cost(u, m.Prices)
	rec.LatencyMS = latency.Milliseconds()
}
