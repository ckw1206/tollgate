package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tollgate/core/llm"
)

func anthropicServer(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) (*httptest.Server, *llm.Model) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "ak" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("path=%s headers=%v", r.URL.Path, r.Header)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		h(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &llm.Model{Name: "claude", Provider: "anthropic", UpstreamModel: "claude-x", APIBase: srv.URL, Secret: []byte("ak")}
}

func TestAnthropic_Chat_TranslatesBothWays(t *testing.T) {
	srv, m := anthropicServer(t, func(w http.ResponseWriter, body map[string]any) {
		if body["system"] != "be brief" || body["max_tokens"] != float64(4096) || body["model"] != "claude-x" {
			t.Errorf("body=%v", body)
		}
		msgs := body["messages"].([]any)
		first := msgs[0].(map[string]any)
		img := first["content"].([]any)[1].(map[string]any)
		src := img["source"].(map[string]any)
		if len(msgs) != 1 || first["role"] != "user" || src["media_type"] != "image/png" || src["data"] != "AAAA" {
			t.Errorf("messages=%v", msgs)
		}
		io.WriteString(w, `{"id":"m1","content":[{"type":"text","text":"hel"},{"type":"text","text":"lo"}],"stop_reason":"max_tokens",
			"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":100,"cache_creation_input_tokens":5}}`)
	})
	res, err := (&Anthropic{HTTP: srv.Client()}).Chat(context.Background(), m, req(t, `{"model":"claude","messages":[
		{"role":"system","content":"be brief"},
		{"role":"user","content":[{"type":"text","text":"what"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage != (llm.Usage{Input: 10, Output: 4, CacheRead: 100, CacheWrite: 5}) {
		t.Fatalf("usage %+v", res.Usage)
	}
	var out struct {
		Model   string
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct{ Content string }
		}
		Usage struct {
			PromptTokens int64 `json:"prompt_tokens"`
		}
	}
	json.Unmarshal(res.Body, &out)
	if out.Model != "claude" || out.Choices[0].Message.Content != "hello" || out.Choices[0].FinishReason != "length" || out.Usage.PromptTokens != 115 {
		t.Fatalf("out=%+v", out)
	}
}

func TestAnthropic_Stream(t *testing.T) {
	srv, m := anthropicServer(t, func(w http.ResponseWriter, body map[string]any) {
		if body["stream"] != true {
			t.Errorf("stream not set")
		}
		for _, ev := range []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":12,"output_tokens":1}}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hi"}}`,
			`event: ping` + "\n" + `data: {"type":"ping"}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		} {
			io.WriteString(w, ev+"\n\n")
		}
	})
	var chunks []string
	u, err := (&Anthropic{HTTP: srv.Client()}).ChatStream(context.Background(), m,
		req(t, `{"model":"claude","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`),
		func(c []byte) error { chunks = append(chunks, string(c)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if u != (llm.Usage{Input: 12, Output: 7}) {
		t.Fatalf("usage %+v", u)
	}
	all := strings.Join(chunks, "\n")
	if len(chunks) != 4 || !strings.Contains(all, `"role":"assistant"`) || !strings.Contains(all, `"content":"Hi"`) ||
		!strings.Contains(all, `"finish_reason":"stop"`) || !strings.Contains(all, `"completion_tokens":7`) {
		t.Fatalf("chunks=%v", chunks)
	}
}

func TestAnthropic_StreamErrorEvent(t *testing.T) {
	srv, m := anthropicServer(t, func(w http.ResponseWriter, _ map[string]any) {
		io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	})
	_, err := (&Anthropic{HTTP: srv.Client()}).ChatStream(context.Background(), m,
		req(t, `{"model":"claude","stream":true,"messages":[{"role":"user","content":"hi"}]}`), func([]byte) error { return nil })
	var ue *llm.UpstreamError
	if !errors.As(err, &ue) || ue.Message != "Overloaded" {
		t.Fatalf("err=%v", err)
	}
}

func TestAnthropic_RejectsToolsAndEmbeddings(t *testing.T) {
	a := &Anthropic{HTTP: http.DefaultClient}
	m := &llm.Model{Provider: "anthropic"}
	if _, err := a.Chat(context.Background(), m, req(t, `{"model":"c","tools":[{}],"messages":[{"role":"user","content":"x"}]}`)); !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("tools: %v", err)
	}
	if _, err := a.Embed(context.Background(), m, []byte(`{}`)); !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("embed: %v", err)
	}
}

// req is a test helper that parses a ChatRequest from JSON bytes
func req(t *testing.T, s string) *llm.ChatRequest {
	t.Helper()
	r, err := llm.ParseChatRequest([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
