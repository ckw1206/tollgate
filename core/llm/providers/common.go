package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"tollgate/core/llm"
)

func post(ctx context.Context, c *http.Client, url string, headers map[string]string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Upstreams close idle keep-alive connections early (uvicorn: 5s) while Go keeps them 90s,
	// so a POST on a reused, already-closed connection fails with EOF. Marking the request
	// idempotent lets the Transport retry it on a fresh connection; an empty value is never
	// sent on the wire. A retry happens only before any response byte, so nothing reached the client.
	req.Header["X-Idempotency-Key"] = []string{}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, &llm.UpstreamError{Status: resp.StatusCode, Message: upstreamMessage(b)}
	}
	return resp, nil
}

func upstreamMessage(b []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if len(b) > 500 {
		b = b[:500]
	}
	return string(b)
}

func usageFromOpenAI(raw json.RawMessage) llm.Usage {
	var u struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		Details          struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	json.Unmarshal(raw, &u)
	return llm.Usage{Input: u.PromptTokens - u.Details.CachedTokens, CacheRead: u.Details.CachedTokens, Output: u.CompletionTokens}
}
