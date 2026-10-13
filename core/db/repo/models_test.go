package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"tollgate/core/db/repo"
)

func TestModels_CRUDAndUniqueName(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	cred, err := s.CreateCredential(ctx, "openai-prod", "api_key", []byte("n"), []byte("c"))
	if err != nil {
		t.Fatal(err)
	}
	in := repo.ModelInput{Name: "gpt-4o", Provider: "openai", UpstreamModel: "gpt-4o", CredentialID: &cred.ID,
		Enabled: true, PriceInputMicros: 2_500_000, PriceOutputMicros: 10_000_000, Extra: json.RawMessage(`{}`)}
	m, err := s.CreateModel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateModel(ctx, in); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	in.PriceInputMicros = 2_000_000
	got, err := s.UpdateModel(ctx, m.ID, in)
	if err != nil || got.PriceInputMicros != 2_000_000 {
		t.Fatalf("update: %+v %v", got, err)
	}
	if err := s.DeleteCredential(ctx, cred.ID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("credential in use must not delete: %v", err)
	}
}
