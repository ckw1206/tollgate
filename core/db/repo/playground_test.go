package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// TestPlaygroundSessions_OwnerScoped pins the scoping contract: every
// playground_sessions operation is WHERE user_id = $1, so a session is
// invisible to anyone but its owner — Get/Update/Delete on someone else's id
// are a plain miss (ErrNotFound), never a cross-user read or write.
func TestPlaygroundSessions_OwnerScoped(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a, err := s.CreateUser(ctx, "a@x.io", "A", "hunter22hunter22", "member", g.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateUser(ctx, "b@x.io", "B", "hunter22hunter22", "member", g.ID)
	if err != nil {
		t.Fatal(err)
	}

	created, err := s.CreatePlaygroundSession(ctx, a.ID, "compare", json.RawMessage(`{"models":["m1","m2"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.UserID != a.ID || created.Name != "compare" || created.UpdatedAt.IsZero() {
		t.Fatalf("created = %+v", created)
	}

	// User B cannot read, rename, or delete A's session: every op is a miss.
	if _, err := s.GetPlaygroundSession(ctx, b.ID, created.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("B get A's session: %v, want ErrNotFound", err)
	}
	if _, err := s.UpdatePlaygroundSession(ctx, b.ID, created.ID, "stolen", nil); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("B update A's session: %v, want ErrNotFound", err)
	}
	if err := s.DeletePlaygroundSession(ctx, b.ID, created.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("B delete A's session: %v, want ErrNotFound", err)
	}

	// A lists the session (newest first); B's list is empty.
	listA, err := s.ListPlaygroundSessions(ctx, a.ID)
	if err != nil || len(listA) != 1 || listA[0].ID != created.ID || listA[0].Name != "compare" {
		t.Fatalf("A list = %+v err=%v, want the one session", listA, err)
	}
	listB, err := s.ListPlaygroundSessions(ctx, b.ID)
	if err != nil || len(listB) != 0 {
		t.Fatalf("B list = %+v err=%v, want empty", listB, err)
	}

	// A's update renames, replaces data, and bumps updated_at.
	time.Sleep(10 * time.Millisecond)
	updated, err := s.UpdatePlaygroundSession(ctx, a.ID, created.ID, "renamed", json.RawMessage(`{"models":["m1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed" || !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated = %+v, want renamed with a later updated_at", updated)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created_at changed: %v -> %v", created.CreatedAt, updated.CreatedAt)
	}
	got, err := s.GetPlaygroundSession(ctx, a.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(got.Data, &data); err != nil || len(data.Models) != 1 || data.Models[0] != "m1" {
		t.Fatalf("data = %s, want {\"models\":[\"m1\"]}", got.Data)
	}
	listA, err = s.ListPlaygroundSessions(ctx, a.ID)
	if err != nil || len(listA) != 1 || listA[0].Name != "renamed" {
		t.Fatalf("A list after update = %+v err=%v, want the renamed session", listA, err)
	}

	// Owner delete works, then the session is gone for the owner too.
	if err := s.DeletePlaygroundSession(ctx, a.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPlaygroundSession(ctx, a.ID, created.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("get after delete: %v, want ErrNotFound", err)
	}
	listA, err = s.ListPlaygroundSessions(ctx, a.ID)
	if err != nil || len(listA) != 0 {
		t.Fatalf("A list after delete = %+v err=%v, want empty", listA, err)
	}
}
