package repo_test

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestRotateKey_CopiesSettingsAndRetiresOld(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	rpm := int32(60)
	exp := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	_, old, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "ci", GroupID: g.ID, Models: []string{"m1"},
		BudgetType: "monthly", BudgetNanos: 10e9, RPMLimit: &rpm, ExpiresAt: &exp})
	if err := s.ExecForTest(ctx, `UPDATE api_keys SET spend_nanos = 4e9 WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}

	raw, nk, retired, err := s.RotateKey(ctx, old.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "tg-") || nk.ID == old.ID || nk.KeyPrefix == old.KeyPrefix {
		t.Fatalf("new key: %+v", nk)
	}
	if nk.Alias != "ci" || nk.UserID != u.ID || nk.GroupID != g.ID || len(nk.Models) != 1 || nk.BudgetType != "monthly" ||
		nk.BudgetNanos != 10e9 || nk.SpendNanos != 4e9 || nk.RPMLimit == nil || *nk.RPMLimit != 60 ||
		nk.ExpiresAt == nil || !nk.ExpiresAt.Equal(exp) || nk.RotatedAt != nil || nk.RevokedAt != nil {
		t.Fatalf("settings not copied: %+v", nk)
	}
	if retired.Alias != "ci (rotated)" || retired.RotatedAt == nil || retired.RevokedAt != nil || retired.ExpiresAt == nil ||
		time.Until(*retired.ExpiresAt) < 59*time.Minute || time.Until(*retired.ExpiresAt) > 61*time.Minute {
		t.Fatalf("old key not retired: %+v", retired)
	}
	if info, err := s.AuthByKeyHash(ctx, repo.HashToken(raw)); err != nil || info.Key.ID != nk.ID {
		t.Fatalf("new key doesn't authenticate: %v", err)
	}
	if _, _, _, err := s.RotateKey(ctx, old.ID, time.Hour); !errors.Is(err, repo.ErrKeyNotRotatable) {
		t.Fatalf("second rotate of the old key: %v", err)
	}

	// Grace 0 = regenerate: the old key is revoked immediately.
	_, _, regen, err := s.RotateKey(ctx, nk.ID, 0)
	if err != nil || regen.RevokedAt == nil {
		t.Fatalf("regenerate: %+v %v", regen, err)
	}
}

func TestRotateKey_SoonerExpiryWins(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	exp := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	_, k, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: "unlimited", ExpiresAt: &exp})
	_, _, retired, err := s.RotateKey(ctx, k.ID, 24*time.Hour)
	if err != nil || !retired.ExpiresAt.Equal(exp) {
		t.Fatalf("expires_at moved later: %+v %v", retired.ExpiresAt, err)
	}
}

func TestRotateKey_RejectsRevokedAndExpired(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	_, revoked, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "r", GroupID: g.ID, BudgetType: "unlimited"})
	s.RevokeKey(ctx, revoked.ID)
	past := time.Now().Add(-time.Minute)
	_, expired, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "e", GroupID: g.ID, BudgetType: "unlimited", ExpiresAt: &past})
	for _, id := range []string{revoked.ID, expired.ID} {
		if _, _, _, err := s.RotateKey(ctx, id, time.Hour); !errors.Is(err, repo.ErrKeyNotRotatable) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, _, _, err := s.RotateKey(ctx, "00000000-0000-0000-0000-000000000000", time.Hour); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}

// Review Focus #6
func TestRotateKey_ConcurrentSingleUse(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	_, k, _ := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: "unlimited"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, other := 0, []error{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := s.RotateKey(ctx, k.ID, time.Hour)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if !errors.Is(err, repo.ErrKeyNotRotatable) {
				other = append(other, err)
			}
		}()
	}
	wg.Wait()
	keys, _ := s.ListKeys(ctx, u.ID)
	if ok != 1 || len(other) != 0 || len(keys) != 2 {
		t.Fatalf("ok=%d unexpected=%v keys=%d", ok, other, len(keys))
	}
}
