package repo_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func admin(t *testing.T, s *repo.Store, g *repo.Group) *repo.User {
	u, err := s.CreateUser(context.Background(), "admin@x.io", "Admin", "hunter22hunter22", "admin", g.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAcceptInvite_AssignsRoleAndGroup(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a := admin(t, s, g)
	team, _ := s.CreateGroup(ctx, repo.GroupInput{Name: "team", BudgetType: "unlimited"})
	token, _, err := s.CreateInvite(ctx, a.ID, "viewer", team.ID, "", 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.AcceptInvite(ctx, token, "New@x.io", "New", "hunter22hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "viewer" || u.GroupID != team.ID {
		t.Fatalf("got %+v", u)
	}
	if _, err := s.AcceptInvite(ctx, token, "again@x.io", "Again", "hunter22hunter22"); !errors.Is(err, repo.ErrInviteUnusable) {
		t.Fatalf("reuse: %v", err)
	}
}

func TestAcceptInvite_RejectsExpiredRevokedAndWrongEmail(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a := admin(t, s, g)

	expired, _, _ := s.CreateInvite(ctx, a.ID, "member", g.ID, "", -time.Minute)
	if _, err := s.AcceptInvite(ctx, expired, "e@x.io", "E", "hunter22hunter22"); !errors.Is(err, repo.ErrInviteUnusable) {
		t.Fatalf("expired: %v", err)
	}

	revoked, inv, _ := s.CreateInvite(ctx, a.ID, "member", g.ID, "", time.Hour)
	s.RevokeInvite(ctx, inv.ID)
	if _, err := s.AcceptInvite(ctx, revoked, "r@x.io", "R", "hunter22hunter22"); !errors.Is(err, repo.ErrInviteUnusable) {
		t.Fatalf("revoked: %v", err)
	}

	bound, _, _ := s.CreateInvite(ctx, a.ID, "member", g.ID, "only@x.io", time.Hour)
	if _, err := s.AcceptInvite(ctx, bound, "other@x.io", "O", "hunter22hunter22"); !errors.Is(err, repo.ErrInviteUnusable) {
		t.Fatalf("wrong email: %v", err)
	}
	// The failed attempt must not burn the invite.
	if _, err := s.AcceptInvite(ctx, bound, "ONLY@x.io", "O", "hunter22hunter22"); err != nil {
		t.Fatalf("right email: %v", err)
	}
}

// Review Focus #2
func TestAcceptInvite_ConcurrentSingleUse(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a := admin(t, s, g)
	token, _, _ := s.CreateInvite(ctx, a.ID, "member", g.ID, "", time.Hour)

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.AcceptInvite(ctx, token, fmt.Sprintf("u%d@x.io", i), "U", "hunter22hunter22"); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d accepts succeeded, want 1", ok)
	}
	users, _ := s.ListUsers(ctx)
	if len(users) != 2 { // admin + one invitee
		t.Fatalf("users=%d", len(users))
	}
}
