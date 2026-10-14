package repo_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
)

func TestCountUsers(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("empty: %d %v", n, err)
	}
	s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "member", g.ID)
	s.CreateUser(ctx, "b@x.io", "B", "hunter22hunter22", "viewer", g.ID)
	if n, err := s.CountUsers(ctx); err != nil || n != 2 {
		t.Fatalf("two: %d %v", n, err)
	}
}

func TestBootstrapAdmin_OnlyOnEmptyTable(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u, err := s.BootstrapAdmin(ctx, " Root@X.io ", "Root", "hunter22hunter22")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "root@x.io" || u.Role != "admin" || u.GroupID != g.ID || !repo.CheckPassword(u, "hunter22hunter22") {
		t.Fatalf("got %+v", u)
	}
	if _, err := s.BootstrapAdmin(ctx, "other@x.io", "Other", "hunter22hunter22"); !errors.Is(err, repo.ErrAlreadySetUp) {
		t.Fatalf("second bootstrap: %v", err)
	}

	s2, g2 := newStore(t) // any existing user blocks it, not just an admin
	s2.CreateUser(ctx, "m@x.io", "M", "hunter22hunter22", "member", g2.ID)
	if _, err := s2.BootstrapAdmin(ctx, "root@x.io", "Root", "hunter22hunter22"); !errors.Is(err, repo.ErrAlreadySetUp) {
		t.Fatalf("with a member: %v", err)
	}
}

func TestBootstrapAdmin_ConcurrentSingleWinner(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	// More callers than CPUs, so bcrypt finishes in waves and the inserts overlap;
	// without the advisory lock this test fails most runs.
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = s.BootstrapAdmin(ctx, fmt.Sprintf("root%d@x.io", i), "Root", "hunter22hunter22") })
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, repo.ErrAlreadySetUp) {
			t.Errorf("loser: %v", err)
		}
	}
	if n, _ := s.CountUsers(ctx); wins != 1 || n != 1 {
		t.Fatalf("wins=%d users=%d", wins, n)
	}
}

func newStore(t *testing.T) (*repo.Store, *repo.Group) {
	s := repo.New(dbtest.New(t))
	g, err := s.GetGroupByName(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	return s, g
}

func TestCreateUser_LowercasesAndHashes(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "Kyle@Example.COM", "Kyle", "hunter22hunter22", "admin", g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "kyle@example.com" || u.PasswordHash == "hunter22hunter22" {
		t.Fatalf("got %+v", u)
	}
	if !repo.CheckPassword(u, "hunter22hunter22") || repo.CheckPassword(u, "wrong") {
		t.Fatal("password check broken")
	}
	if _, err := s.CreateUser(ctx, "kyle@example.com", "Dup", "hunter22hunter22", "member", g.ID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestUpdateUser_Partial(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "member", g.ID)
	role, disabled := "viewer", true
	got, err := s.UpdateUser(ctx, u.ID, repo.UserUpdate{Role: &role, Disabled: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "viewer" || !got.Disabled || got.Name != "A" || got.ExtraModels == nil || len(got.ExtraModels) != 0 {
		t.Fatalf("got %+v", got)
	}
	got, _ = s.UpdateUser(ctx, u.ID, repo.UserUpdate{ExtraModels: &[]string{"m"}})
	got, _ = s.UpdateUser(ctx, u.ID, repo.UserUpdate{Role: &role}) // nil extra_models keeps the list
	if len(got.ExtraModels) != 1 || got.ExtraModels[0] != "m" {
		t.Fatalf("extra_models not kept: %+v", got)
	}
	if got, _ = s.UpdateUser(ctx, u.ID, repo.UserUpdate{ExtraModels: &[]string{}}); len(got.ExtraModels) != 0 {
		t.Fatalf("[] should clear: %+v", got)
	}
	if _, err := s.GetUser(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
