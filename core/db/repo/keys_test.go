package repo_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func TestGenerateKey_Format(t *testing.T) {
	raw, hash, prefix := repo.GenerateKey()
	if !strings.HasPrefix(raw, "tg-") || len(raw) != 46 || prefix != raw[:12] || hash != repo.HashToken(raw) {
		t.Fatalf("raw=%q prefix=%q", raw, prefix)
	}
}

func TestCreateKey_AuthLookupAndRevoke(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	raw, k, err := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "laptop", GroupID: g.ID, BudgetType: "daily", BudgetNanos: 5e9})
	if err != nil {
		t.Fatal(err)
	}
	if k.BudgetResetAt == nil || !k.BudgetResetAt.After(time.Now()) {
		t.Fatalf("daily key needs a future reset: %+v", k.BudgetResetAt)
	}
	info, err := s.AuthByKeyHash(ctx, repo.HashToken(raw))
	if err != nil || info.Key.ID != k.ID || info.Group.ID != g.ID || info.UserDisabled {
		t.Fatalf("auth: %+v %v", info, err)
	}
	if err := s.RevokeKey(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	info, _ = s.AuthByKeyHash(ctx, repo.HashToken(raw))
	if info.Key.RevokedAt == nil {
		t.Fatal("revoked_at not visible to auth lookup")
	}
	if _, err := s.AuthByKeyHash(ctx, repo.HashToken("tg-nope")); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestListKeys_ScopedByUser(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a := admin(t, s, g)
	m, _ := s.CreateUser(ctx, "m@x.io", "M", "hunter22hunter22", "member", g.ID)
	s.CreateKey(ctx, a.ID, repo.KeyInput{Alias: "a", GroupID: g.ID, BudgetType: "unlimited"})
	s.CreateKey(ctx, m.ID, repo.KeyInput{Alias: "m", GroupID: g.ID, BudgetType: "unlimited"})
	mine, _ := s.ListKeys(ctx, m.ID)
	all, _ := s.ListKeys(ctx, "")
	if len(mine) != 1 || mine[0].Alias != "m" || len(all) != 2 {
		t.Fatalf("mine=%d all=%d", len(mine), len(all))
	}
}
