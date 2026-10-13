package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tollgate/core/llm"
)

func TestGoogle_AIStudio_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-x:generateContent" || r.Header.Get("x-goog-api-key") != "gk" {
			t.Errorf("path=%s key=%q", r.URL.Path, r.Header.Get("x-goog-api-key"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		sys := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)
		contents := body["contents"].([]any)
		gc := body["generationConfig"].(map[string]any)
		if sys["text"] != "be brief" || len(contents) != 2 || contents[1].(map[string]any)["role"] != "model" || gc["maxOutputTokens"] != float64(50) {
			t.Errorf("body=%v", body)
		}
		io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":40,"cachedContentTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":20}}`)
	}))
	defer srv.Close()
	m := &llm.Model{Name: "gem", Provider: "google", UpstreamModel: "gemini-x", APIBase: srv.URL + "/v1beta", CredentialKind: "api_key", Secret: []byte("gk")}
	res, err := (&Google{HTTP: srv.Client()}).Chat(context.Background(), m, req(t, `{"model":"gem","max_tokens":50,"messages":[
		{"role":"system","content":"be brief"},{"role":"user","content":"hello"},{"role":"assistant","content":"yo"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage != (llm.Usage{Input: 30, CacheRead: 10, Output: 25}) {
		t.Fatalf("usage %+v", res.Usage)
	}
}

func TestGoogle_Vertex_StreamURLAndToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/v1/projects/proj/locations/us-east5/publishers/google/models/gemini-x:streamGenerateContent"
		if r.URL.Path != want || r.URL.Query().Get("alt") != "sse" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("path=%s q=%s auth=%q", r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"))
		}
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":1}}\n\n")
		io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]},\"finishReason\":\"MAX_TOKENS\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}\n\n")
	}))
	defer srv.Close()
	g := &Google{HTTP: srv.Client(), TokenFor: func(context.Context, []byte) (string, error) { return "tok", nil }}
	m := &llm.Model{Name: "gem", Provider: "google", UpstreamModel: "gemini-x", APIBase: srv.URL, CredentialKind: "gcp_service_account",
		Secret: []byte(`{}`), Extra: llm.Extra{VertexProject: "proj", VertexLocation: "us-east5"}}
	var n int
	u, err := g.ChatStream(context.Background(), m, req(t, `{"model":"gem","stream":true,"messages":[{"role":"user","content":"x"}]}`),
		func([]byte) error { n++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	// role + 2 text + finish
	if n != 4 || u != (llm.Usage{Input: 3, Output: 2}) {
		t.Fatalf("n=%d usage=%+v", n, u)
	}
}

func TestGoogle_VertexRequiresProject(t *testing.T) {
	g := &Google{HTTP: http.DefaultClient, TokenFor: func(context.Context, []byte) (string, error) { return "t", nil }}
	m := &llm.Model{Provider: "google", UpstreamModel: "x", CredentialKind: "gcp_service_account"}
	if _, err := g.Chat(context.Background(), m, req(t, `{"model":"g","messages":[{"role":"user","content":"x"}]}`)); !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("err=%v", err)
	}
}
