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
	cred, _ := s.CreateCredential(ctx, "openai-prod", "api_key", nonce, ct)
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
