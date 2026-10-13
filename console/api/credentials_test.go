package consoleapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func TestCredentials_ProviderEndpointValidation(t *testing.T) {
	e := newTenv(t)
	post := func(body map[string]any) *httptest.ResponseRecorder {
		return e.as("admin@x.io", "POST", "/console/api/v1/credentials", body)
	}
	// OpenAI-compatible on a private host: plain http and no key are fine.
	w := post(map[string]any{"name": "vllm", "provider": "openai", "api_base": "http://10.0.0.5:8000/v1"})
	c := decodeInto[repo.Credential](t, w)
	if w.Code != 201 || c.Provider != "openai" || c.APIBase != "http://10.0.0.5:8000/v1" || c.Kind != "api_key" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = post(map[string]any{"name": "vx", "kind": "gcp_service_account", "secret": `{"type":"service_account"}`, "vertex_project": "p1"})
	if v := decodeInto[repo.Credential](t, w); w.Code != 201 || v.Provider != "google" || v.VertexLocation != "us-central1" {
		t.Fatalf("vertex: %d %s", w.Code, w.Body)
	}
	for name, body := range map[string]map[string]any{
		"public http":         {"provider": "openai", "api_base": "http://api.example.com/v1"},
		"trailing slash":      {"provider": "openai", "api_base": "https://api.example.com/v1/"},
		"not a URL":           {"provider": "openai", "api_base": "api.example.com"},
		"query":               {"provider": "openai", "api_base": "https://api.example.com/v1?x=1"},
		"anthropic no key":    {"provider": "anthropic"},
		"unknown provider":    {"provider": "cohere", "secret": "k"},
		"vertex not google":   {"provider": "anthropic", "kind": "gcp_service_account", "secret": "{}", "vertex_project": "p"},
		"vertex no project":   {"kind": "gcp_service_account", "secret": "{}"},
		"vertex invalid json": {"kind": "gcp_service_account", "secret": "nope", "vertex_project": "p"},
	} {
		body["name"] = "bad"
		if w := post(body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}

	// A model can't use a credential made for another provider, and its own api_base is validated too.
	m := map[string]any{"name": "claude", "provider": "anthropic", "upstream_model": "x", "credential_id": c.ID}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", m); w.Code != 400 || !strings.Contains(w.Body.String(), "is for openai") {
		t.Fatalf("provider mismatch: %d %s", w.Code, w.Body)
	}
	m["provider"], m["api_base"] = "openai", "http://203.0.113.9/v1"
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", m); w.Code != 400 {
		t.Fatalf("model public http: %d", w.Code)
	}
	delete(m, "api_base")
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", m); w.Code != 201 {
		t.Fatalf("model with blank api_base: %d %s", w.Code, w.Body)
	}
}

func TestCredentials_TestConnection(t *testing.T) {
	e := newTenv(t)
	const key = "test-key-0123456789"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/v1/models":
			w.WriteHeader(404)
		case r.Header.Get("Authorization") == "Bearer slow":
			time.Sleep(time.Second)
		case r.Header.Get("Authorization") != "Bearer "+key:
			w.WriteHeader(401) // echoes the key back, as some upstreams do
			w.Write([]byte(`{"error":{"message":"invalid key ` + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + `"}}`))
		default:
			w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
		}
	}))
	defer up.Close()
	test := func(email, secret string) (int, map[string]any) {
		w := e.as(email, "POST", "/console/api/v1/credentials/test",
			map[string]any{"provider": "openai", "api_base": up.URL + "/v1", "secret": secret})
		if w.Code != 200 {
			return w.Code, nil
		}
		return w.Code, decodeInto[map[string]any](t, w)
	}

	if _, r := test("admin@x.io", key); r["ok"] != true || r["status"] != 200.0 || r["model_count"] != 2.0 || r["message"] != "" {
		t.Fatalf("ok: %v", r)
	}
	const wrong = "wrong-key-abcdefgh"
	if _, r := test("admin@x.io", wrong); r["ok"] != false || r["status"] != 401.0 ||
		strings.Contains(r["message"].(string), wrong) || !strings.Contains(r["message"].(string), "[redacted]") {
		t.Fatalf("401: %v", r)
	}
	old := credentialTestTimeout
	credentialTestTimeout = 100 * time.Millisecond
	_, r := test("admin@x.io", "slow")
	credentialTestTimeout = old
	if r["ok"] != false || r["status"] != 0.0 || r["message"] != "timed out after 100ms" {
		t.Fatalf("timeout: %v", r)
	}
	if code, _ := test("member@x.io", key); code != 403 {
		t.Fatalf("member: %d", code)
	}
	// Testing never stores anything.
	if cs, _ := e.store.ListCredentials(t.Context()); len(cs) != 0 {
		t.Fatalf("stored: %v", cs)
	}
	// Same endpoint rules as saving: a public plain-http base is refused before any request.
	w := e.as("admin@x.io", "POST", "/console/api/v1/credentials/test", map[string]any{"provider": "openai", "api_base": "http://example.com/v1"})
	if w.Code != 400 {
		t.Fatalf("public http: %d", w.Code)
	}
}

func TestRedactSecret_ServiceAccountKey(t *testing.T) {
	sa := `{"type":"service_account","private_key_id":"kid-123456","private_key":"-----BEGIN-----\nAAAA\n-----END-----\n"}`
	got := redactSecret("bad key -----BEGIN-----\nAAAA\n-----END-----\n (kid-123456)", sa)
	if strings.Contains(got, "AAAA") || strings.Contains(got, "kid-123456") {
		t.Fatal(got)
	}
}
