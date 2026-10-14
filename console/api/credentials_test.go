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
	// OpenAI-compatible over plain http with no key is fine.
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

	// A model can't use a credential made for another provider.
	m := map[string]any{"name": "claude", "provider": "anthropic", "upstream_model": "x", "credential_id": c.ID}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", m); w.Code != 400 || !strings.Contains(w.Body.String(), "is for openai") {
		t.Fatalf("provider mismatch: %d %s", w.Code, w.Body)
	}
	// With a credential, the model's own api_base is dropped on create and update: the credential's endpoint wins.
	m["provider"], m["api_base"] = "openai", "https://own.example.com/v1"
	w = e.as("admin@x.io", "POST", "/console/api/v1/models", m)
	created := decodeInto[repo.Model](t, w)
	if w.Code != 201 || created.APIBase != "" {
		t.Fatalf("create with credential: %d %s", w.Code, w.Body)
	}
	if w := e.as("admin@x.io", "PUT", "/console/api/v1/models/"+created.ID, m); w.Code != 200 || decodeInto[repo.Model](t, w).APIBase != "" {
		t.Fatalf("update with credential: %d %s", w.Code, w.Body)
	}
	// Without one, api_base is required and validated.
	own := map[string]any{"name": "own", "provider": "openai", "upstream_model": "x"}
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", own); w.Code != 400 || !strings.Contains(w.Body.String(), "api_base is required") {
		t.Fatalf("keyless without api_base: %d %s", w.Code, w.Body)
	}
	own["api_base"] = "https://api.example.com/v1/"
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", own); w.Code != 400 {
		t.Fatalf("model trailing slash: %d", w.Code)
	}
	own["api_base"] = "http://10.0.0.5:8000/v1"
	if w := e.as("admin@x.io", "POST", "/console/api/v1/models", own); w.Code != 201 || decodeInto[repo.Model](t, w).APIBase != "http://10.0.0.5:8000/v1" {
		t.Fatalf("keyless with api_base: %d %s", w.Code, w.Body)
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
	// Same endpoint rules as saving: an invalid base is refused before any request.
	w := e.as("admin@x.io", "POST", "/console/api/v1/credentials/test", map[string]any{"provider": "openai", "api_base": "ftp://example.com/v1"})
	if w.Code != 400 {
		t.Fatalf("invalid base: %d", w.Code)
	}
}

func TestRedactSecret_ServiceAccountKey(t *testing.T) {
	sa := `{"type":"service_account","private_key_id":"kid-123456","private_key":"-----BEGIN-----\nAAAA\n-----END-----\n"}`
	got := redactSecret("bad key -----BEGIN-----\nAAAA\n-----END-----\n (kid-123456)", sa)
	if strings.Contains(got, "AAAA") || strings.Contains(got, "kid-123456") {
		t.Fatal(got)
	}
}

func TestCredentials_Update(t *testing.T) {
	e := newTenv(t)
	key := "first-key-0123456789"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"data":[{"id":"a"}]}`))
	}))
	defer up.Close()
	put := func(id string, body map[string]any) *httptest.ResponseRecorder {
		return e.as("admin@x.io", "PUT", "/console/api/v1/credentials/"+id, body)
	}
	// testSaved runs Test connection with the saved secret, as the Edit dialog does.
	testSaved := func(id string) map[string]any {
		return decodeInto[map[string]any](t, e.as("admin@x.io", "POST", "/console/api/v1/credentials/test",
			map[string]any{"id": id, "api_base": up.URL}))
	}

	c := decodeInto[repo.Credential](t, e.as("admin@x.io", "POST", "/console/api/v1/credentials",
		map[string]any{"name": "c1", "provider": "openai", "api_base": up.URL, "secret": key}))
	e.as("admin@x.io", "POST", "/console/api/v1/models", map[string]any{"name": "m1", "provider": "openai", "upstream_model": "x", "credential_id": c.ID})
	list := decodeInto[[]repo.Credential](t, e.as("admin@x.io", "GET", "/console/api/v1/credentials", nil))
	if len(list) != 1 || len(list[0].UsedBy) != 1 || list[0].UsedBy[0] != "m1" || list[0].SecretUpdatedAt.IsZero() {
		t.Fatalf("list: %+v", list)
	}

	// Rename + new endpoint, secret kept: still decrypts (the name is the secret's AAD) and reloads the registry.
	reloads := e.reloads
	w := put(c.ID, map[string]any{"name": "c2", "api_base": "https://other.example.com/v1"})
	got := decodeInto[repo.Credential](t, w)
	if w.Code != 200 || got.Name != "c2" || got.APIBase != "https://other.example.com/v1" || !got.SecretUpdatedAt.Equal(list[0].SecretUpdatedAt) || e.reloads != reloads+1 {
		t.Fatalf("keep: %d %s reloads=%d", w.Code, w.Body, e.reloads-reloads)
	}
	if r := testSaved(c.ID); r["ok"] != true {
		t.Fatalf("saved secret after rename: %v", r)
	}

	// Replace the secret.
	key = "second-key-9876543210"
	w = put(c.ID, map[string]any{"secret": key})
	if got := decodeInto[repo.Credential](t, w); w.Code != 200 || !got.SecretUpdatedAt.After(list[0].SecretUpdatedAt) || got.Name != "c2" {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	if r := testSaved(c.ID); r["ok"] != true {
		t.Fatalf("replaced secret not used: %v", r)
	}
	if strings.Contains(e.as("admin@x.io", "GET", "/console/api/v1/credentials", nil).Body.String(), key) {
		t.Fatal("list leaks the secret")
	}

	e.as("admin@x.io", "POST", "/console/api/v1/credentials", map[string]any{"name": "c3", "provider": "openai"})
	for name, tc := range map[string]struct {
		body map[string]any
		code int
	}{
		"provider change": {map[string]any{"provider": "anthropic"}, 400},
		"same provider":   {map[string]any{"provider": "openai"}, 200},
		"duplicate name":  {map[string]any{"name": "c3"}, 409},
		"empty name":      {map[string]any{"name": ""}, 400},
		"bad endpoint":    {map[string]any{"api_base": "https://x.example.com/v1/"}, 400},
	} {
		if w := put(c.ID, tc.body); w.Code != tc.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := put("00000000-0000-0000-0000-000000000000", map[string]any{"name": "x"}); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	if w := e.as("member@x.io", "PUT", "/console/api/v1/credentials/"+c.ID, map[string]any{"name": "x"}); w.Code != 403 {
		t.Fatalf("member: %d", w.Code)
	}

	// Audit records which fields changed, never their values.
	entries, _, _ := e.store.ListAudit(t.Context(), repo.AuditFilter{Limit: 50})
	var updates []string
	for _, a := range entries {
		if a.Action != "credential.update" {
			continue
		}
		d := string(a.Details)
		if strings.Contains(d, "key-") || strings.Contains(d, "other.example.com") || strings.Contains(d, `"c2"`) {
			t.Fatalf("audit has values: %s", d)
		}
		updates = append(updates, strings.ReplaceAll(d, " ", ""))
	}
	// keep+rename, replace secret, and the no-op "same provider" save
	if len(updates) != 3 || !strings.Contains(strings.Join(updates, " "), `["name","api_base"]`) || !strings.Contains(strings.Join(updates, " "), `["secret"]`) {
		t.Fatalf("audit: %v", updates)
	}
}
