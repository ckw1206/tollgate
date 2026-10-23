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

// Provider default endpoints, used when a model's credential (or a model without one) sets no api_base.
// The console prefills the same values (web/src/components/CredentialDialog.tsx).
const (
	DefaultOpenAIBase    = "https://api.openai.com/v1"
	DefaultAnthropicBase = "https://api.anthropic.com"
	DefaultGoogleBase    = "https://generativelanguage.googleapis.com/v1beta"
)

// probeList is the decoded shape of an upstream model list. The three slices are the
// OpenAI/Anthropic, Gemini API and Vertex spellings of the same thing; a provider fills
// exactly one. The shared max_model_len lives alongside each entry rather than in it,
// because the entry is kept raw for the caller that needs to pick one by id.
type probeList struct {
	Data            []json.RawMessage `json:"data"`            // OpenAI, Anthropic
	Models          []json.RawMessage `json:"models"`          // Gemini API
	PublisherModels []json.RawMessage `json:"publisherModels"` // Vertex
}

// count is the reachability signal Probe reports: every entry the upstream listed.
func (l probeList) count() int { return len(l.Data) + len(l.Models) + len(l.PublisherModels) }

// Probe lists the upstream's models with m's endpoint and credential: the console's
// "Test connection". It returns the HTTP status and model count; failures are
// *llm.UpstreamError, and status 0 means the upstream couldn't be reached.
func Probe(ctx context.Context, c *http.Client, m *llm.Model) (status, count int, err error) {
	status, body, err := probeListModels(ctx, c, m)
	if err != nil {
		return status, 0, err
	}
	var list probeList
	if json.Unmarshal(body, &list) != nil {
		return status, 0, &llm.UpstreamError{Status: status, Message: "upstream returned non-JSON body"}
	}
	return status, list.count(), nil
}

// ProbeLimits is Probe plus the upstream's reported context length for m: the console
// compares it against the operator-entered context_window. It matches the list entry
// whose id equals m.UpstreamModel.
//
// maxModelLen == 0 means "the upstream did not report a limit" and is not an error: a
// non-OpenAI-compatible upstream, no matching entry, or an absent/null/non-numeric field
// all yield 0. Failures are *llm.UpstreamError for non-2xx statuses, non-JSON bodies and
// unreachable upstreams, exactly as in Probe; status 0 means the upstream couldn't be
// reached.
func ProbeLimits(ctx context.Context, c *http.Client, m *llm.Model) (status int, maxModelLen int64, err error) {
	status, body, err := probeListModels(ctx, c, m)
	if err != nil {
		return status, 0, err
	}
	var list probeList
	if json.Unmarshal(body, &list) != nil {
		return status, 0, &llm.UpstreamError{Status: status, Message: "upstream returned non-JSON body"}
	}
	return status, maxModelLenFor(list, m.UpstreamModel), nil
}

// probeListModels performs the model-list GET shared by Probe and ProbeLimits. On success
// it returns the status and the raw body; on failure it returns an *llm.UpstreamError with
// the status (0 when the upstream couldn't be reached).
func probeListModels(ctx context.Context, c *http.Client, m *llm.Model) (status int, body []byte, err error) {
	url, h, err := probeRequest(ctx, m)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, &llm.UpstreamError{Message: err.Error()}
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, &llm.UpstreamError{Message: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, nil, &llm.UpstreamError{Status: resp.StatusCode, Message: upstreamMessage(b)}
	}
	return resp.StatusCode, b, nil
}

// maxPlausibleContextLen bounds a reported context length. Real models sit far below it
// (1e6 is already an extreme long-context model), so anything larger is a malformed or
// hostile value rather than a limit, and is ignored instead of trusting it.
const maxPlausibleContextLen = 1 << 32 // ~4.29e9 tokens

// maxModelLenFor picks the entry whose id equals upstreamModel and returns its
// max_model_len, or 0 when there is no such entry or the field is absent, null or not a
// number. Matching is exact first, then case-insensitive; anything else is 0 rather than a
// guess, since an entry chosen by mistake would report another model's limit.
func maxModelLenFor(list probeList, upstreamModel string) int64 {
	entries := make([]json.RawMessage, 0, list.count())
	entries = append(entries, list.Data...)
	entries = append(entries, list.Models...)
	entries = append(entries, list.PublisherModels...)
	var fold int64
	for _, raw := range entries {
		var e struct {
			ID          string   `json:"id"`
			MaxModelLen *float64 `json:"max_model_len"`
		}
		if json.Unmarshal(raw, &e) != nil || e.MaxModelLen == nil {
			continue // not an object, or reports no limit
		}
		// Decoding through float64 accepts both 32768 and 32768.0. A string or bool
		// fails the unmarshal and leaves the limit unset, which reads as "no limit".
		// A value no real context could hold (a fraction like 0.5, or an out-of-range
		// literal like 1e100, whose float->int conversion is implementation-defined) is
		// likewise treated as "no limit" rather than reported: a nonsense number must not
		// silently become a limit so large that it disables the caller's check.
		if f := *e.MaxModelLen; f >= 1 && f <= maxPlausibleContextLen {
			if n := int64(f); n > 0 {
				switch {
				case e.ID == upstreamModel && upstreamModel != "":
					return n
				case strings.EqualFold(e.ID, upstreamModel) && upstreamModel != "" && fold == 0:
					fold = n // keep looking for an exact match
				}
			}
		}
	}
	return fold
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
