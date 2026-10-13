package repo_test

import (
	"context"
	"encoding/json"
	"testing"

	"tollgate/core/db/repo"
)

func TestAudit_InsertListPaginate(t *testing.T) {
	s, g := newStore(t)
	ctx := context.Background()
	a := admin(t, s, g)
	for _, act := range []string{"key.create", "key.revoke", "model.update"} {
		if err := s.InsertAudit(ctx, repo.AuditEntry{ActorID: &a.ID, ActorEmail: a.Email, Action: act, Resource: "x", Details: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := s.ListAudit(ctx, repo.AuditFilter{Limit: 2})
	if err != nil || total != 3 || len(page) != 2 || page[0].Action != "model.update" {
		t.Fatalf("page=%+v total=%d err=%v", page, total, err)
	}
	only, total, _ := s.ListAudit(ctx, repo.AuditFilter{Action: "key.create", Limit: 50})
	if total != 1 || only[0].Action != "key.create" {
		t.Fatalf("filter: %+v", only)
	}
	if _, total, _ := s.ListAudit(ctx, repo.AuditFilter{Action: "key.", Limit: 50}); total != 2 {
		t.Fatalf("prefix filter matched %d, want 2", total)
	}
}
