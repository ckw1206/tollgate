package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"tollgate/core/llm"
)

type Google struct {
	HTTP     *http.Client
	TokenFor func(ctx context.Context, serviceAccountJSON []byte) (string, error)
}

func NewGoogle(c *http.Client) *Google {
	var mu sync.Mutex
	sources := map[[32]byte]oauth2.TokenSource{}
	return &Google{HTTP: c, TokenFor: func(ctx context.Context, sa []byte) (string, error) {
		key := sha256.Sum256(sa)
		mu.Lock()
		ts, ok := sources[key]
		if !ok {
			creds, err := google.CredentialsFromJSONWithType(context.Background(), sa, google.ServiceAccount, "https://www.googleapis.com/auth/cloud-platform")
			if err != nil {
				mu.Unlock()
				return "", fmt.Errorf("service account: %w", err)
			}
			ts = creds.TokenSource
			sources[key] = ts
		}
		mu.Unlock()
		tok, err := ts.Token()
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	}}
}

func (g *Google) endpoint(ctx context.Context, m *llm.Model, stream bool) (string, map[string]string, error) {
	method, query := "generateContent", ""
	if stream {
		method, query = "streamGenerateContent", "?alt=sse"
	}
	h := map[string]string{}
	for k, v := range m.Extra.Headers {
		h[k] = v
	}
	if m.CredentialKind == "gcp_service_account" {
		if m.Extra.VertexProject == "" {
			return "", nil, fmt.Errorf("%w: model %s needs extra.vertex_project", llm.ErrBadRequest, m.Name)
		}
		loc := m.Extra.VertexLocation
		if loc == "" {
			loc = "us-central1"
		}
		base := m.APIBase
		if base == "" {
			base = vertexBase(loc)
		}
		tok, err := g.TokenFor(ctx, m.Secret)
		if err != nil {
			return "", nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: err.Error()}
		}
		h["Authorization"] = "Bearer " + tok
		return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:%s%s",
			strings.TrimRight(base, "/"), m.Extra.VertexProject, loc, m.UpstreamModel, method, query), h, nil
	}
	base := m.APIBase
	if base == "" {
		base = DefaultGoogleBase
	}
	h["x-goog-api-key"] = string(m.Secret)
	return fmt.Sprintf("%s/models/%s:%s%s", strings.TrimRight(base, "/"), m.UpstreamModel, method, query), h, nil
}

func vertexBase(loc string) string {
	if loc == "global" {
		return "https://aiplatform.googleapis.com"
	}
	return "https://" + loc + "-aiplatform.googleapis.com"
}

func googleBody(req *llm.ChatRequest) ([]byte, error) {
	if req.HasTools {
		return nil, fmt.Errorf("%w: tool calling is not supported for google models yet", llm.ErrBadRequest)
	}
	var contents []map[string]any
	for _, msg := range req.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			continue
		}
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		for _, p := range msg.Parts {
			switch p.Type {
			case "text":
				parts = append(parts, map[string]any{"text": p.Text})
			case "image_url":
				mime, data, ok := splitDataURL(p.ImageURL)
				if !ok {
					return nil, fmt.Errorf("%w: google models only accept data: image URLs", llm.ErrBadRequest)
				}
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}})
			}
		}
		if len(parts) > 0 {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	b := map[string]any{"contents": contents}
	if s := req.SystemText(); s != "" {
		b["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": s}}}
	}
	gc := map[string]any{}
	if req.Temperature != nil {
		gc["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		gc["topP"] = *req.TopP
	}
	if req.MaxTokens != nil {
		gc["maxOutputTokens"] = *req.MaxTokens
	}
	if len(req.Stop) > 0 {
		gc["stopSequences"] = req.Stop
	}
	if len(gc) > 0 {
		b["generationConfig"] = gc
	}
	return json.Marshal(b)
}

type googleResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount        int64 `json:"promptTokenCount"`
		CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
		CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
		ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

func (r *googleResp) usage() llm.Usage {
	u := r.UsageMetadata
	return llm.Usage{Input: u.PromptTokenCount - u.CachedContentTokenCount, CacheRead: u.CachedContentTokenCount, Output: u.CandidatesTokenCount + u.ThoughtsTokenCount}
}

func (r *googleResp) text() string {
	var b strings.Builder
	if len(r.Candidates) > 0 {
		for _, p := range r.Candidates[0].Content.Parts {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func googleFinish(reason string) string {
	switch reason {
	case "":
		return ""
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "content_filter"
	}
	return "stop"
}

func (g *Google) Chat(ctx context.Context, m *llm.Model, req *llm.ChatRequest) (*llm.Result, error) {
	body, err := googleBody(req)
	if err != nil {
		return nil, err
	}
	url, h, err := g.endpoint(ctx, m, false)
	if err != nil {
		return nil, err
	}
	resp, err := post(ctx, g.HTTP, url, h, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r googleResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: "upstream returned non-JSON body"}
	}
	finish := "stop"
	if len(r.Candidates) > 0 {
		finish = googleFinish(r.Candidates[0].FinishReason)
	}
	u := r.usage()
	return &llm.Result{Body: llm.Completion(m.Name, r.text(), finish, u), Usage: u}, nil
}

func (g *Google) ChatStream(ctx context.Context, m *llm.Model, req *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	var u llm.Usage
	body, err := googleBody(req)
	if err != nil {
		return u, err
	}
	url, h, err := g.endpoint(ctx, m, true)
	if err != nil {
		return u, err
	}
	resp, err := post(ctx, g.HTTP, url, h, body)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	ch := llm.NewChunker(m.Name)
	if err := emit(ch.Role()); err != nil {
		return u, err
	}
	err = llm.ReadSSE(resp.Body, func(_, data string) error {
		var r googleResp
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return fmt.Errorf("bad upstream chunk: %w", err)
		}
		u = r.usage()
		if t := r.text(); t != "" {
			if err := emit(ch.Text(t)); err != nil {
				return err
			}
		}
		if len(r.Candidates) > 0 {
			if f := googleFinish(r.Candidates[0].FinishReason); f != "" {
				return emit(ch.Finish(f))
			}
		}
		return nil
	})
	if err == nil && req.IncludeUsage {
		err = emit(ch.Usage(u))
	}
	return u, err
}

func (g *Google) Embed(context.Context, *llm.Model, []byte) (*llm.Result, error) {
	return nil, fmt.Errorf("%w: embeddings are only supported for openai-compatible models", llm.ErrBadRequest)
}
