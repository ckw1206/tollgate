package consoleapi

import (
	"context"
	"strings"
	"testing"

	"tollgate/core/db/repo"
)

type created struct {
	Key repo.APIKey
	Raw string
}

func TestKeys_CreateReturnsRawOnceAndAuditsWithoutSecret(t *testing.T) {
	e := newTenv(t)
	w := e.as("member@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "laptop", "budget_type": "unlimited"})
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	c := decodeInto[created](t, w)
	if !strings.HasPrefix(c.Raw, "tg-") || c.Key.UserID != e.member.ID || c.Key.GroupID != e.group.ID {
		t.Fatalf("%+v", c)
	}
	list := e.as("member@x.io", "GET", "/console/api/v1/keys", nil).Body.String()
	if strings.Contains(list, c.Raw) || strings.Contains(list, "key_hash") {
		t.Fatal("list leaks key material")
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "key.create", Limit: 10})
	if len(entries) != 1 || strings.Contains(string(entries[0].Details), c.Raw) {
		t.Fatalf("audit: %+v", entries)
	}
}

// Review Focus #1
func TestKeys_MemberCannotTouchOthersKey(t *testing.T) {
	e := newTenv(t)
	c := decodeInto[created](t, e.as("admin@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "admin-key", "budget_type": "unlimited"}))
	id := c.Key.ID
	if w := e.as("member@x.io", "PUT", "/console/api/v1/keys/"+id, map[string]any{"alias": "pwned", "budget_type": "unlimited"}); w.Code != 404 {
		t.Fatalf("update other's key: %d", w.Code)
	}
	if w := e.as("member@x.io", "DELETE", "/console/api/v1/keys/"+id, nil); w.Code != 404 {
		t.Fatalf("revoke other's key: %d", w.Code)
	}
	if list := decodeInto[[]repo.APIKey](t, e.as("member@x.io", "GET", "/console/api/v1/keys", nil)); len(list) != 0 {
		t.Fatalf("member sees %d keys", len(list))
	}
	k, _ := e.store.GetKey(context.Background(), id)
	if k.Alias != "admin-key" || k.RevokedAt != nil {
		t.Fatalf("key modified: %+v", k)
	}
}

func TestKeys_MemberCannotMoveKeyToAnotherGroup(t *testing.T) {
	e := newTenv(t)
	other, _ := e.store.CreateGroup(context.Background(), repo.GroupInput{Name: "rich", BudgetType: "unlimited"})
	c := decodeInto[created](t, e.as("member@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "k", "budget_type": "unlimited", "group_id": other.ID}))
	if c.Key.GroupID != e.group.ID {
		t.Fatal("member billed a key to another group")
	}
}

func TestKeys_ViewerCannotCreate(t *testing.T) {
	e := newTenv(t)
	if w := e.as("viewer@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "k", "budget_type": "unlimited"}); w.Code != 403 {
		t.Fatalf("%d", w.Code)
	}
}

func TestKeys_ValidatesBudgetType(t *testing.T) {
	e := newTenv(t)
	if w := e.as("member@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "k", "budget_type": "weekly"}); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
}

type rotated struct {
	Raw string
	Key repo.APIKey
	Old repo.APIKey
}

func TestKeys_Rotate(t *testing.T) {
	e := newTenv(t)
	mine := decodeInto[created](t, e.as("member@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "ci", "budget_type": "unlimited"}))
	theirs := decodeInto[created](t, e.as("admin@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "adm", "budget_type": "unlimited"}))
	path := func(id string) string { return "/console/api/v1/keys/" + id + "/rotate" }

	for _, body := range []any{map[string]any{}, map[string]any{"grace_seconds": -1}, map[string]any{"grace_seconds": 604801}} {
		if w := e.as("member@x.io", "POST", path(mine.Key.ID), body); w.Code != 400 {
			t.Fatalf("bad grace %v: %d", body, w.Code)
		}
	}
	if w := e.as("member@x.io", "POST", path(theirs.Key.ID), map[string]any{"grace_seconds": 3600}); w.Code != 404 {
		t.Fatalf("member rotated admin's key: %d", w.Code)
	}
	if w := e.as("viewer@x.io", "POST", path(mine.Key.ID), map[string]any{"grace_seconds": 3600}); w.Code != 403 {
		t.Fatalf("viewer: %d", w.Code)
	}

	w := e.as("member@x.io", "POST", path(mine.Key.ID), map[string]any{"grace_seconds": 3600})
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := decodeInto[rotated](t, w)
	if !strings.HasPrefix(r.Raw, "tg-") || r.Key.Alias != "ci" || r.Old.Alias != "ci (rotated)" || r.Old.RotatedAt == nil {
		t.Fatalf("%+v", r)
	}
	if w := e.as("member@x.io", "POST", path(mine.Key.ID), map[string]any{"grace_seconds": 3600}); w.Code != 400 {
		t.Fatalf("rotating a rotated key: %d", w.Code)
	}
	// The server, not just the UI, refuses edits that would push the grace expiry out.
	if w := e.as("member@x.io", "PUT", "/console/api/v1/keys/"+r.Old.ID, map[string]any{"alias": "x", "budget_type": "unlimited", "expires_at": nil}); w.Code != 400 {
		t.Fatalf("edited a rotated key: %d", w.Code)
	}
	if k, _ := e.store.GetKey(context.Background(), r.Old.ID); k.ExpiresAt == nil || k.Alias != "ci (rotated)" {
		t.Fatalf("rotated key changed: %+v", k)
	}
	entries, _, _ := e.store.ListAudit(context.Background(), repo.AuditFilter{Action: "key.rotate", Limit: 10})
	if len(entries) != 1 || strings.Contains(string(entries[0].Details), r.Raw) {
		t.Fatalf("audit: %+v", entries)
	}
}

func TestKeys_AdminRotateKeepsOwner(t *testing.T) {
	e := newTenv(t)
	c := decodeInto[created](t, e.as("member@x.io", "POST", "/console/api/v1/keys", map[string]any{"alias": "m", "budget_type": "unlimited"}))
	r := decodeInto[rotated](t, e.as("admin@x.io", "POST", "/console/api/v1/keys/"+c.Key.ID+"/rotate", map[string]any{"grace_seconds": 0}))
	if r.Key.UserID != e.member.ID || r.Old.RevokedAt == nil {
		t.Fatalf("owner=%s revoked=%v", r.Key.UserID, r.Old.RevokedAt)
	}
}

func TestMe_ReturnsBuildVersion(t *testing.T) {
	e := newTenv(t)
	type server struct {
		ServerName string `json:"server_name"`
		Version    string
		Commit     string
	}
	me := decodeInto[struct{ Server server }](t, e.as("viewer@x.io", "GET", "/auth/me", nil))
	if me.Server.Version != "v0.0.0-test" || len(me.Server.Commit) != 40 || me.Server.ServerName == "" {
		t.Fatalf("%+v", me.Server)
	}
}
