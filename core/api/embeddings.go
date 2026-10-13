package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"tollgate/core/llm"
)

func (s *Server) embeddings(w http.ResponseWriter, r *http.Request) {
	info := authFrom(r.Context())
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		WriteError(w, 413, "invalid_request_error", "request_too_large", "request body too large")
		return
	}
	var head struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &head) != nil || head.Model == "" {
		WriteError(w, 400, "invalid_request_error", "invalid_request", "model is required")
		return
	}
	m, p, ok := s.Registry.Get(head.Model)
	if !ok {
		WriteError(w, 404, "invalid_request_error", "model_not_found", fmt.Sprintf("model %q not found", head.Model))
		return
	}
	if !modelAllowed(info, m.Name) {
		WriteError(w, 403, "permission_error", "model_not_allowed", fmt.Sprintf("this key may not use model %q", m.Name))
		return
	}
	if !s.admit(w, r, info) {
		return
	}
	start := s.Now()
	rec := newRecord(info, m, start)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), upstreamTimeout)
	defer cancel()
	res, err := p.Embed(ctx, m, body)
	if errors.Is(err, llm.ErrBadRequest) {
		WriteError(w, 400, "invalid_request_error", "unsupported", err.Error())
		return
	}
	var u llm.Usage
	if err != nil {
		rec.Status, rec.ErrorMessage = "error", err.Error()
		rec.HTTPStatus = writeUpstreamError(w, err)
	} else {
		u = res.Usage
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tollgate-Cost", llm.FormatUSD(llm.Cost(u, m.Prices)))
		w.WriteHeader(200)
		w.Write(res.Body)
	}
	fill(&rec, u, m, s.Now().Sub(start))
	s.finish(info, rec, u)
}
