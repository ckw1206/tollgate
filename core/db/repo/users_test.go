package repo_test

import (
	"context"
	"errors"
	"testing"

	"tollgate/core/db/dbtest"
	"tollgate/core/db/repo"
)

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
	if got.Role != "viewer" || !got.Disabled || got.Name != "A" {
		t.Fatalf("got %+v", got)
	}
	if _, err := s.GetUser(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
