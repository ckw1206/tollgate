package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"tollgate/core/llm"
)

type OpenAI struct {
	HTTP *http.Client
}

func (p *OpenAI) url(m *llm.Model, path string) string {
	base := m.APIBase
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return strings.TrimRight(base, "/") + path
}

func (p *OpenAI) headers(m *llm.Model) map[string]string {
	h := map[string]string{}
	for k, v := range m.Extra.Headers {
		h[k] = v
	}
	if len(m.Secret) > 0 {
		h["Authorization"] = "Bearer " + string(m.Secret)
	}
	return h
}

func body(raw map[string]json.RawMessage, upstreamModel string, stream bool) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(raw)+1)
	for k, v := range raw {
		out[k] = v
	}
	out["model"], _ = json.Marshal(upstreamModel)
	if stream {
		so := map[string]any{}
		json.Unmarshal(out["stream_options"], &so)
		so["include_usage"] = true
		out["stream_options"], _ = json.Marshal(so)
	}
	return json.Marshal(out)
}

func (p *OpenAI) Chat(ctx context.Context, m *llm.Model, req *llm.ChatRequest) (*llm.Result, error) {
	b, err := body(req.Raw, m.UpstreamModel, false)
	if err != nil {
		return nil, err
	}
	resp, err := post(ctx, p.HTTP, p.url(m, "/chat/completions"), p.headers(m), b)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	var env struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: "upstream returned non-JSON body"}
	}
	return &llm.Result{Body: out, Usage: usageFromOpenAI(env.Usage)}, nil
}

func (p *OpenAI) ChatStream(ctx context.Context, m *llm.Model, req *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	var u llm.Usage
	b, err := body(req.Raw, m.UpstreamModel, true)
	if err != nil {
		return u, err
	}
	resp, err := post(ctx, p.HTTP, p.url(m, "/chat/completions"), p.headers(m), b)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	err = llm.ReadSSE(resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var c struct {
			Choices []json.RawMessage `json:"choices"`
			Usage   json.RawMessage   `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return fmt.Errorf("bad upstream chunk: %w", err)
		}
		if len(c.Usage) > 0 && string(c.Usage) != "null" {
			u = usageFromOpenAI(c.Usage)
			if len(c.Choices) == 0 && !req.IncludeUsage {
				return nil // we injected include_usage; the client didn't ask for this chunk
			}
		}
		return emit([]byte(data))
	})
	return u, err
}

func (p *OpenAI) Embed(ctx context.Context, m *llm.Model, raw []byte) (*llm.Result, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", llm.ErrBadRequest)
	}
	b, _ := body(in, m.UpstreamModel, false)
	resp, err := post(ctx, p.HTTP, p.url(m, "/embeddings"), p.headers(m), b)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: err.Error()}
	}
	var env struct {
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		} `json:"usage"`
	}
	json.Unmarshal(out, &env)
	return &llm.Result{Body: out, Usage: llm.Usage{Input: env.Usage.PromptTokens}}, nil
}
