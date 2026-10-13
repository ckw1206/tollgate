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

type Anthropic struct {
	HTTP *http.Client
}

const anthropicDefaultMaxTokens = 4096

func splitDataURL(u string) (mime, data string, ok bool) {
	rest, found := strings.CutPrefix(u, "data:")
	if !found {
		return "", "", false
	}
	meta, data, found := strings.Cut(rest, ",")
	mime, isB64 := strings.CutSuffix(meta, ";base64")
	return mime, data, found && isB64
}

func anthropicBody(m *llm.Model, req *llm.ChatRequest, stream bool) ([]byte, error) {
	if req.HasTools {
		return nil, fmt.Errorf("%w: tool calling is not supported for anthropic models yet", llm.ErrBadRequest)
	}
	var msgs []map[string]any
	for _, msg := range req.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			continue
		}
		var content []map[string]any
		for _, p := range msg.Parts {
			switch p.Type {
			case "text":
				content = append(content, map[string]any{"type": "text", "text": p.Text})
			case "image_url":
				if mime, data, ok := splitDataURL(p.ImageURL); ok {
					content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mime, "data": data}})
				} else {
					content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": p.ImageURL}})
				}
			}
		}
		if len(content) == 0 {
			continue
		}
		msgs = append(msgs, map[string]any{"role": msg.Role, "content": content})
	}
	b := map[string]any{"model": m.UpstreamModel, "messages": msgs, "max_tokens": anthropicDefaultMaxTokens}
	if req.MaxTokens != nil {
		b["max_tokens"] = *req.MaxTokens
	}
	if s := req.SystemText(); s != "" {
		b["system"] = s
	}
	if req.Temperature != nil {
		b["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		b["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		b["stop_sequences"] = req.Stop
	}
	if stream {
		b["stream"] = true
	}
	return json.Marshal(b)
}

func (p *Anthropic) send(ctx context.Context, m *llm.Model, body []byte) (*http.Response, error) {
	base := m.APIBase
	if base == "" {
		base = "https://api.anthropic.com"
	}
	h := map[string]string{"x-api-key": string(m.Secret), "anthropic-version": "2023-06-01"}
	for k, v := range m.Extra.Headers {
		h[k] = v
	}
	return post(ctx, p.HTTP, strings.TrimRight(base, "/")+"/v1/messages", h, body)
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (a anthropicUsage) usage() llm.Usage {
	return llm.Usage{Input: a.InputTokens, Output: a.OutputTokens, CacheRead: a.CacheReadInputTokens, CacheWrite: a.CacheCreationInputTokens}
}

func anthropicFinish(stop string) string {
	if stop == "max_tokens" {
		return "length"
	}
	return "stop"
}

func (p *Anthropic) Chat(ctx context.Context, m *llm.Model, req *llm.ChatRequest) (*llm.Result, error) {
	body, err := anthropicBody(m, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.send(ctx, m, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var r struct {
		Content []struct {
			Type, Text string
		} `json:"content"`
		StopReason string         `json:"stop_reason"`
		Usage      anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, &llm.UpstreamError{Status: http.StatusBadGateway, Message: "upstream returned non-JSON body"}
	}
	var text strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	u := r.Usage.usage()
	return &llm.Result{Body: llm.Completion(m.Name, text.String(), anthropicFinish(r.StopReason), u), Usage: u}, nil
}

func (p *Anthropic) ChatStream(ctx context.Context, m *llm.Model, req *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	var u llm.Usage
	body, err := anthropicBody(m, req, true)
	if err != nil {
		return u, err
	}
	resp, err := p.send(ctx, m, body)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	ch := llm.NewChunker(m.Name)
	err = llm.ReadSSE(resp.Body, func(_, data string) error {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage anthropicUsage `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return fmt.Errorf("bad upstream event: %w", err)
		}
		switch ev.Type {
		case "message_start":
			u = ev.Message.Usage.usage()
			return emit(ch.Role())
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" {
				return emit(ch.Text(ev.Delta.Text))
			}
		case "message_delta":
			u.Output = ev.Usage.OutputTokens // cumulative
			if ev.Delta.StopReason != "" {
				return emit(ch.Finish(anthropicFinish(ev.Delta.StopReason)))
			}
		case "error":
			return &llm.UpstreamError{Status: http.StatusBadGateway, Message: ev.Error.Message}
		}
		return nil
	})
	if err == nil && req.IncludeUsage {
		err = emit(ch.Usage(u))
	}
	return u, err
}

func (p *Anthropic) Embed(context.Context, *llm.Model, []byte) (*llm.Result, error) {
	return nil, fmt.Errorf("%w: embeddings are only supported for openai-compatible models", llm.ErrBadRequest)
}
