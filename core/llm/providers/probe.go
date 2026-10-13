package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2/google"

	"tollgate/core/llm"
)

// Provider default endpoints, used when neither the model nor its credential sets api_base.
// The console prefills the same values (web/src/components/CredentialDialog.tsx).
const (
	DefaultOpenAIBase    = "https://api.openai.com/v1"
	DefaultAnthropicBase = "https://api.anthropic.com"
	DefaultGoogleBase    = "https://generativelanguage.googleapis.com/v1beta"
)

// Probe lists the upstream's models with m's endpoint and credential: the console's
// "Test connection". It returns the HTTP status and model count; failures are
// *llm.UpstreamError, and status 0 means the upstream couldn't be reached.
func Probe(ctx context.Context, c *http.Client, m *llm.Model) (status, count int, err error) {
	url, h, err := probeRequest(ctx, m)
	if err != nil {
		return 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, &llm.UpstreamError{Message: err.Error()}
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, 0, &llm.UpstreamError{Message: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, 0, &llm.UpstreamError{Status: resp.StatusCode, Message: upstreamMessage(b)}
	}
	var list struct {
		Data            []json.RawMessage `json:"data"`            // OpenAI, Anthropic
		Models          []json.RawMessage `json:"models"`          // Gemini API
		PublisherModels []json.RawMessage `json:"publisherModels"` // Vertex
	}
	if json.Unmarshal(b, &list) != nil {
		return resp.StatusCode, 0, &llm.UpstreamError{Status: resp.StatusCode, Message: "upstream returned non-JSON body"}
	}
	return resp.StatusCode, len(list.Data) + len(list.Models) + len(list.PublisherModels), nil
}

// ponytail: one page of models (up to 1000); the count is a reachability signal, not an inventory.
func probeRequest(ctx context.Context, m *llm.Model) (string, map[string]string, error) {
	base := strings.TrimRight(m.APIBase, "/")
	h := map[string]string{}
	switch {
	case m.Provider == "openai":
		if base == "" {
			base = DefaultOpenAIBase
		}
		if len(m.Secret) > 0 {
			h["Authorization"] = "Bearer " + string(m.Secret)
		}
		return base + "/models", h, nil
	case m.Provider == "anthropic":
		if base == "" {
			base = DefaultAnthropicBase
		}
		h["x-api-key"], h["anthropic-version"] = string(m.Secret), "2023-06-01"
		return base + "/v1/models?limit=1000", h, nil
	case m.CredentialKind == "gcp_service_account":
		if base == "" {
			loc := m.Extra.VertexLocation
			if loc == "" {
				loc = "us-central1"
			}
			base = vertexBase(loc)
		}
		creds, err := google.CredentialsFromJSONWithType(ctx, m.Secret, google.ServiceAccount, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return "", nil, &llm.UpstreamError{Message: "service account: " + err.Error()}
		}
		tok, err := creds.TokenSource.Token()
		if err != nil {
			return "", nil, &llm.UpstreamError{Message: "service account token: " + err.Error()}
		}
		h["Authorization"], h["x-goog-user-project"] = "Bearer "+tok.AccessToken, m.Extra.VertexProject
		return base + "/v1beta1/publishers/google/models?pageSize=1000", h, nil
	default: // google, Gemini API key
		if base == "" {
			base = DefaultGoogleBase
		}
		h["x-goog-api-key"] = string(m.Secret)
		return base + "/models?pageSize=1000", h, nil
	}
}
