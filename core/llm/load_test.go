package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
	"tollgate/core/vault"
)

func TestLoadModels_DecryptsAndSkipsDisabled(t *testing.T) {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	v, _ := vault.New(bytes.Repeat([]byte{1}, 32))
	nonce, ct, _ := v.Seal("openai-prod", []byte("sk-live"))
	cred, _ := s.CreateCredential(ctx, repo.CredentialInput{Name: "openai-prod", Kind: "api_key", Provider: "openai"}, nonce, ct)
	s.CreateModel(ctx, repo.ModelInput{Name: "on", Provider: "openai", UpstreamModel: "gpt-4o", CredentialID: &cred.ID, Enabled: true,
		PriceInputMicros: 1, Extra: json.RawMessage(`{"headers":{"X-Org":"me"}}`)})
	s.CreateModel(ctx, repo.ModelInput{Name: "off", Provider: "openai", UpstreamModel: "x", Enabled: false})

	ms, err := llm.LoadModels(ctx, s, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Name != "on" || string(ms[0].Secret) != "sk-live" || ms[0].CredentialKind != "api_key" || ms[0].Extra.Headers["X-Org"] != "me" {
		t.Fatalf("got %+v", ms)
	}

	reg := llm.NewRegistry(map[string]llm.Provider{"openai": nil})
	reg.Replace(ms)
	if _, _, ok := reg.Get("on"); !ok {
		t.Fatal("registry miss")
	}
	if _, _, ok := reg.Get("off"); ok {
		t.Fatal("disabled model in registry")
	}
}

// Endpoint resolution: credential.api_base ▸ provider default ("" here); the model's own
// api_base counts without a credential, or (legacy) when its credential has none.
func TestLoadModels_EndpointFromCredential(t *testing.T) {
	s := repo.New(dbtest.New(t))
	ctx := context.Background()
	v, _ := vault.New(bytes.Repeat([]byte{1}, 32))
	nonce, ct, _ := v.Seal("vllm", []byte(""))
	vllm, _ := s.CreateCredential(ctx, repo.CredentialInput{Name: "vllm", Kind: "api_key", Provider: "openai", APIBase: "http://10.0.0.5:8000/v1"}, nonce, ct)
	nonce, ct, _ = v.Seal("plain", []byte("k"))
	plain, _ := s.CreateCredential(ctx, repo.CredentialInput{Name: "plain", Kind: "api_key", Provider: "openai"}, nonce, ct)
	nonce, ct, _ = v.Seal("vx", []byte(`{}`))
	vx, _ := s.CreateCredential(ctx, repo.CredentialInput{Name: "vx", Kind: "gcp_service_account", Provider: "google",
		VertexProject: "cred-proj", VertexLocation: "europe-west4"}, nonce, ct)
	for _, in := range []repo.ModelInput{
		{Name: "inherits", Provider: "openai", UpstreamModel: "m", CredentialID: &vllm.ID, Enabled: true},
		{Name: "overrides", Provider: "openai", UpstreamModel: "m", CredentialID: &vllm.ID, APIBase: "https://own.example.com/v1", Enabled: true},
		{Name: "no-cred", Provider: "openai", UpstreamModel: "m", APIBase: "http://10.0.0.9:8000/v1", Enabled: true},
		{Name: "default", Provider: "openai", UpstreamModel: "m", CredentialID: &plain.ID, Enabled: true},
		{Name: "legacy", Provider: "openai", UpstreamModel: "m", CredentialID: &plain.ID, APIBase: "https://proxy.example.com/v1", Enabled: true},
		{Name: "vertex", Provider: "google", UpstreamModel: "g", CredentialID: &vx.ID, Enabled: true},
		{Name: "vertex-own", Provider: "google", UpstreamModel: "g", CredentialID: &vx.ID, Enabled: true,
			Extra: json.RawMessage(`{"vertex_project":"model-proj"}`)},
	} {
		if _, err := s.CreateModel(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	ms, err := llm.LoadModels(ctx, s, v)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*llm.Model{}
	for _, m := range ms {
		got[m.Name] = m
	}
	for name, want := range map[string]string{"inherits": "http://10.0.0.5:8000/v1", "overrides": "http://10.0.0.5:8000/v1",
		"no-cred": "http://10.0.0.9:8000/v1", "default": "", "legacy": "https://proxy.example.com/v1"} {
		if got[name].APIBase != want {
			t.Errorf("%s api_base: %q, want %q", name, got[name].APIBase, want)
		}
	}
	if x := got["vertex"].Extra; x.VertexProject != "cred-proj" || x.VertexLocation != "europe-west4" {
		t.Fatalf("vertex: %+v", x)
	}
	if x := got["vertex-own"].Extra; x.VertexProject != "model-proj" || x.VertexLocation != "europe-west4" {
		t.Fatalf("vertex-own: %+v", x)
	}
}
