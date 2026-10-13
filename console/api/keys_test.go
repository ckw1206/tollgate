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
