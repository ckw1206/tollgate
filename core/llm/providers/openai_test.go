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

func upstream(t *testing.T, h func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Org") != "me" {
			t.Errorf("extra header missing")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		h(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func model(base string) *llm.Model {
	return &llm.Model{Name: "fast", Provider: "openai", UpstreamModel: "gpt-4o-mini", APIBase: base,
		Secret: []byte("sk-test"), Extra: llm.Extra{Headers: map[string]string{"X-Org": "me"}}}
}

func chatReq(t *testing.T, body string) *llm.ChatRequest {
	r, err := llm.ParseChatRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenAI_Chat_RewritesModelAndNormalizesUsage(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, body map[string]any) {
		if body["model"] != "gpt-4o-mini" || body["seed"] != float64(7) {
			t.Errorf("body = %v", body)
		}
		io.WriteString(w, `{"id":"x","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":30}}}`)
	})
	res, err := (&OpenAI{HTTP: srv.Client()}).Chat(context.Background(), model(srv.URL), chatReq(t, `{"model":"fast","seed":7,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage != (llm.Usage{Input: 70, CacheRead: 30, Output: 20}) {
		t.Fatalf("usage %+v", res.Usage)
	}
}

func TestOpenAI_Stream_InjectsAndStripsUsage(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, body map[string]any) {
		so, _ := body["stream_options"].(map[string]any)
		if so["include_usage"] != true {
			t.Errorf("include_usage not injected: %v", body)
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"y\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})
	var chunks []string
	u, err := (&OpenAI{HTTP: srv.Client()}).ChatStream(context.Background(), model(srv.URL),
		chatReq(t, `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		func(c []byte) error { chunks = append(chunks, string(c)); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("usage chunk should be stripped when client didn't ask: %v", chunks)
	}
	if u != (llm.Usage{Input: 5, Output: 2}) {
		t.Fatalf("usage %+v", u)
	}
}

func TestOpenAI_Stream_KeepsUsageWhenClientAsked(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, _ map[string]any) {
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	})
	n := 0
	(&OpenAI{HTTP: srv.Client()}).ChatStream(context.Background(), model(srv.URL),
		chatReq(t, `{"model":"fast","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`),
		func([]byte) error { n++; return nil })
	if n != 1 {
		t.Fatalf("emitted %d", n)
	}
}

func TestOpenAI_UpstreamErrorGarbageBody(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(503)
		io.WriteString(w, strings.Repeat("<html>oops", 200))
	})
	_, err := (&OpenAI{HTTP: srv.Client()}).Chat(context.Background(), model(srv.URL), chatReq(t, `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`))
	var ue *llm.UpstreamError
	if !errors.As(err, &ue) || ue.Status != 503 || len(ue.Message) > 512 {
		t.Fatalf("err=%v", err)
	}
}

func TestOpenAI_Embed(t *testing.T) {
	srv := upstream(t, func(w http.ResponseWriter, body map[string]any) {
		if body["model"] != "gpt-4o-mini" {
			t.Errorf("model not rewritten")
		}
		io.WriteString(w, `{"data":[{"embedding":[0.1]}],"usage":{"prompt_tokens":3,"total_tokens":3}}`)
	})
	res, err := (&OpenAI{HTTP: srv.Client()}).Embed(context.Background(), model(srv.URL), []byte(`{"model":"fast","input":"hi"}`))
	if err != nil || res.Usage.Input != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
