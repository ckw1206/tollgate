package llm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

func OpenAIUsage(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":         u.PromptTokens(),
		"completion_tokens":     u.Output,
		"total_tokens":          u.Tokens(),
		"prompt_tokens_details": map[string]any{"cached_tokens": u.CacheRead},
	}
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

// Completion builds a non-streaming chat.completion body for translated providers.
func Completion(model, text, finish string, u Usage) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": newID(), "object": "chat.completion", "created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": finish,
			"message": map[string]any{"role": "assistant", "content": text},
		}},
		"usage": OpenAIUsage(u),
	})
	return b
}

// Chunker builds chat.completion.chunk bodies sharing one id/created.
type Chunker struct {
	id, model string
	created   int64
}

func NewChunker(model string) *Chunker {
	return &Chunker{id: newID(), model: model, created: time.Now().Unix()}
}

func (c *Chunker) chunk(choices []any, usage any) []byte {
	m := map[string]any{"id": c.id, "object": "chat.completion.chunk", "created": c.created, "model": c.model, "choices": choices}
	if usage != nil {
		m["usage"] = usage
	}
	b, _ := json.Marshal(m)
	return b
}

func (c *Chunker) Role() []byte {
	return c.chunk([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}}, nil)
}

func (c *Chunker) Text(s string) []byte {
	return c.chunk([]any{map[string]any{"index": 0, "delta": map[string]any{"content": s}, "finish_reason": nil}}, nil)
}

func (c *Chunker) Finish(reason string) []byte {
	return c.chunk([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}}, nil)
}

func (c *Chunker) Usage(u Usage) []byte { return c.chunk([]any{}, OpenAIUsage(u)) }
