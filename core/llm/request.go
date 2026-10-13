package llm

import (
	"encoding/json"
	"fmt"
)

type Part struct {
	Type     string // "text" | "image_url"
	Text     string
	ImageURL string
}

type Message struct {
	Role  string
	Parts []Part
}

type ChatRequest struct {
	Raw          map[string]json.RawMessage // original body, forwarded as-is to OpenAI-compatible upstreams
	Model        string
	Stream       bool
	IncludeUsage bool // client asked for stream_options.include_usage
	HasTools     bool
	Messages     []Message
	Temperature  *float64
	TopP         *float64
	MaxTokens    *int
	Stop         []string
}

type wireMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

func ParseChatRequest(body []byte) (*ChatRequest, error) {
	r := &ChatRequest{}
	if err := json.Unmarshal(body, &r.Raw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	var w struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
		Messages            []wireMessage   `json:"messages"`
		Temperature         *float64        `json:"temperature"`
		TopP                *float64        `json:"top_p"`
		MaxTokens           *int            `json:"max_tokens"`
		MaxCompletionTokens *int            `json:"max_completion_tokens"`
		Stop                json.RawMessage `json:"stop"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	if w.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(w.Messages) == 0 {
		return nil, fmt.Errorf("messages is required")
	}
	r.Model, r.Stream, r.Temperature, r.TopP = w.Model, w.Stream, w.Temperature, w.TopP
	r.IncludeUsage = w.StreamOptions != nil && w.StreamOptions.IncludeUsage
	r.MaxTokens = w.MaxCompletionTokens
	if r.MaxTokens == nil {
		r.MaxTokens = w.MaxTokens
	}
	_, hasTools := r.Raw["tools"]
	_, hasFuncs := r.Raw["functions"]
	r.HasTools = hasTools || hasFuncs

	if len(w.Stop) > 0 && string(w.Stop) != "null" {
		var one string
		if json.Unmarshal(w.Stop, &one) == nil {
			r.Stop = []string{one}
		} else if err := json.Unmarshal(w.Stop, &r.Stop); err != nil {
			return nil, fmt.Errorf("stop must be a string or array of strings")
		}
	}

	for i, m := range w.Messages {
		if m.Role == "tool" || m.Role == "function" || (len(m.ToolCalls) > 0 && string(m.ToolCalls) != "null") {
			r.HasTools = true
		}
		msg := Message{Role: m.Role}
		switch {
		case len(m.Content) == 0 || string(m.Content) == "null":
		case m.Content[0] == '"':
			var s string
			json.Unmarshal(m.Content, &s)
			msg.Parts = []Part{{Type: "text", Text: s}}
		case m.Content[0] == '[':
			var parts []wirePart
			if err := json.Unmarshal(m.Content, &parts); err != nil {
				return nil, fmt.Errorf("messages[%d].content: %w", i, err)
			}
			for _, p := range parts {
				switch p.Type {
				case "text":
					msg.Parts = append(msg.Parts, Part{Type: "text", Text: p.Text})
				case "image_url":
					msg.Parts = append(msg.Parts, Part{Type: "image_url", ImageURL: p.ImageURL.URL})
				}
			}
		default:
			return nil, fmt.Errorf("messages[%d].content must be a string or array", i)
		}
		r.Messages = append(r.Messages, msg)
	}
	return r, nil
}

// SystemText joins all system/developer message text, for providers with a separate system field.
func (r *ChatRequest) SystemText() string {
	var out string
	for _, m := range r.Messages {
		if m.Role != "system" && m.Role != "developer" {
			continue
		}
		for _, p := range m.Parts {
			if p.Type == "text" {
				if out != "" {
					out += "\n\n"
				}
				out += p.Text
			}
		}
	}
	return out
}
