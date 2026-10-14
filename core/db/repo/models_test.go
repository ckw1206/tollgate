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
	cred, err := s.CreateCredential(ctx, repo.CredentialInput{Name: "openai-prod", Kind: "api_key", Provider: "openai"}, []byte("n"), []byte("c"))
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

func i64(v int64) *int64 { return &v }

func TestModels_ContextWindowOptional(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	cred, _ := s.CreateCredential(ctx, repo.CredentialInput{Name: "openai-cw", Kind: "api_key", Provider: "openai"}, []byte("n"), []byte("c"))
	base := repo.ModelInput{Name: "cw", Provider: "openai", UpstreamModel: "gpt-4o", CredentialID: &cred.ID,
		Enabled: true, PriceInputMicros: 1, PriceOutputMicros: 1, Extra: json.RawMessage(`{}`)}

	// (i) created with a value: reads back pointing at 128000
	in := base
	in.ContextWindow = i64(128000)
	m, err := s.CreateModel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextWindow == nil || *m.ContextWindow != 128000 {
		t.Fatalf("create: ContextWindow=%v, want 128000", m.ContextWindow)
	}

	// (iii) created without: reads back nil (NULL default)
	nb := base
	nb.Name = "cw-none"
	m2, err := s.CreateModel(ctx, nb)
	if err != nil {
		t.Fatal(err)
	}
	if m2.ContextWindow != nil {
		t.Fatalf("unset: ContextWindow=%v, want nil", *m2.ContextWindow)
	}

	// (ii) updating it persists a new value
	in.ContextWindow = i64(256000)
	updated, err := s.UpdateModel(ctx, m.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ContextWindow == nil || *updated.ContextWindow != 256000 {
		t.Fatalf("update: ContextWindow=%v, want 256000", updated.ContextWindow)
	}

	// (iv) updating with nil clears it back to nil
	in.ContextWindow = nil
	cleared, err := s.UpdateModel(ctx, m.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ContextWindow != nil {
		t.Fatalf("clear: ContextWindow=%v, want nil", *cleared.ContextWindow)
	}
}
