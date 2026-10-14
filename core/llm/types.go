package llm

import (
	"context"
	"errors"
	"fmt"
)

type Usage struct {
	Input, Output, CacheRead, CacheWrite int64
}

// PromptTokens is what clients see as usage.prompt_tokens.
func (u Usage) PromptTokens() int64 { return u.Input + u.CacheRead + u.CacheWrite }
func (u Usage) Tokens() int64       { return u.PromptTokens() + u.Output }

type Prices struct {
	InputMicros, OutputMicros         int64
	CacheReadMicros, CacheWriteMicros *int64
}

type Extra struct {
	VertexProject  string            `json:"vertex_project"`
	VertexLocation string            `json:"vertex_location"`
	Headers        map[string]string `json:"headers"`
}

// Model is the runtime form of a models row with its credential decrypted.
type Model struct {
	ID, Name, Provider, UpstreamModel, APIBase string
	CredentialKind                             string // "", "api_key", "gcp_service_account"
	Secret                                     []byte
	Prices                                     Prices
	ContextWindow                              int64 // total context budget in tokens; 0 = unset
	Extra                                      Extra
	Created                                    int64
}

// Result is an OpenAI-format response body plus normalized usage.
type Result struct {
	Body  []byte
	Usage Usage
}

type Provider interface {
	Chat(ctx context.Context, m *Model, req *ChatRequest) (*Result, error)
	// ChatStream calls emit with each OpenAI-format chunk JSON (no "data: " prefix).
	// It returns the usage seen so far even when it also returns an error.
	ChatStream(ctx context.Context, m *Model, req *ChatRequest, emit func(chunk []byte) error) (Usage, error)
	Embed(ctx context.Context, m *Model, body []byte) (*Result, error)
}

// UpstreamError is a non-2xx (or unreachable) provider response.
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string { return fmt.Sprintf("upstream %d: %s", e.Status, e.Message) }

// ErrBadRequest wraps request problems detected before calling upstream (unsupported features etc.).
var ErrBadRequest = errors.New("bad request")
