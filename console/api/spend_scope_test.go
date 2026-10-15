package consoleapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// TestSpendScope_NewGroupByStaysScoped is Review Focus #2 for the new group_by
// values: a caller without spend:read_all must only ever see their own spend,
// for ANY group_by, and a user_id query param must not widen the scope.
func TestSpendScope_NewGroupByStaysScoped(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	m, _ := e.store.CreateModel(ctx, repo.ModelInput{Name: "m", Provider: "openai", UpstreamModel: "m", Enabled: true, Extra: json.RawMessage(`{}`)})
	// The admin's spend must land in a DISTINCT group from the member's (both
	// are in "default"), so group_by=group is a real two-bucket assertion.
	ag, _ := e.store.CreateGroup(ctx, repo.GroupInput{Name: "admin-only", BudgetType: "unlimited"})
	_, mk, _ := e.store.CreateKey(ctx, e.member.ID, repo.KeyInput{Alias: "mk", GroupID: e.group.ID, BudgetType: "unlimited"})
	_, ak, _ := e.store.CreateKey(ctx, e.admin.ID, repo.KeyInput{Alias: "ak", GroupID: ag.ID, BudgetType: "unlimited"})
	at := time.Now()
	e.store.RecordSpend(ctx, []repo.SpendRecord{
		{KeyID: mk.ID, UserID: e.member.ID, GroupID: e.group.ID, ModelID: m.ID, ModelName: "m",
			Provider: "openai", CostNanos: 100, Status: "success", HTTPStatus: 200, CreatedAt: at},
		{KeyID: ak.ID, UserID: e.admin.ID, GroupID: ag.ID, ModelID: m.ID, ModelName: "m",
			Provider: "openai", CostNanos: 200, Status: "success", HTTPStatus: 200, CreatedAt: at},
	})
	from := at.Add(-time.Hour).UTC().Format(time.RFC3339)
	to := at.Add(time.Hour).UTC().Format(time.RFC3339)
	q := "from=" + from + "&to=" + to
	type out struct{ Rows []repo.SpendRow }

	// Each new group_by: the member sees exactly their own row.
	for _, gb := range []string{"user", "group", "hour", "day", "week", "month", "year", "model,hour", "model,week", "model,month", "model,year"} {
		got := decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by="+gb+"&"+q, nil))
		if len(got.Rows) != 1 {
			t.Fatalf("group_by=%s member: got %+v, want exactly one (own) row", gb, got)
		}
	}

	// user: the row's Bucket is the member's id.
	got := decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=user&"+q, nil))
	if got.Rows[0].Bucket != e.member.ID {
		t.Fatalf("user: Bucket=%s, want %s", got.Rows[0].Bucket, e.member.ID)
	}
	// group: the row's Bucket is the member's group id (NOT the admin's group).
	got = decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=group&"+q, nil))
	if got.Rows[0].Bucket != e.group.ID || got.Rows[0].CostNanos != 100 {
		t.Fatalf("group: %+v, want bucket %s cost 100", got.Rows[0], e.group.ID)
	}
	// The server override wins over a user_id query param: the member still
	// sees only their own rows even when asking for the admin's spend.
	for _, gb := range []string{"user", "group"} {
		got = decodeInto[out](t, e.as("member@x.io", "GET", "/console/api/v1/spend?group_by="+gb+"&user_id="+e.admin.ID+"&"+q, nil))
		if len(got.Rows) != 1 {
			t.Fatalf("group_by=%s user_id=admin: got %+v, want exactly one (own) row", gb, got)
		}
		if gb == "user" && got.Rows[0].Bucket != e.member.ID {
			t.Fatalf("user_id=admin: Bucket=%s, want %s (override wins)", got.Rows[0].Bucket, e.member.ID)
		}
		if gb == "group" && got.Rows[0].Bucket != e.group.ID {
			t.Fatalf("user_id=admin group: Bucket=%s, want %s (override wins)", got.Rows[0].Bucket, e.group.ID)
		}
	}
	// The admin (spend:read_all) sees both users' and both groups' rows.
	got = decodeInto[out](t, e.as("admin@x.io", "GET", "/console/api/v1/spend?group_by=user&"+q, nil))
	if len(got.Rows) != 2 {
		t.Fatalf("admin user: got %+v, want two rows", got)
	}
	got = decodeInto[out](t, e.as("admin@x.io", "GET", "/console/api/v1/spend?group_by=group&"+q, nil))
	if len(got.Rows) != 2 {
		t.Fatalf("admin group: got %+v, want two rows", got)
	}
	// The new values are not 400 for a member (week used to be rejected):
	// a valid response, not a bad request.
	if w := e.as("member@x.io", "GET", "/console/api/v1/spend?group_by=week&"+q, nil); w.Code != http.StatusOK {
		t.Fatalf("week: %d %s, want 200", w.Code, w.Body)
	}
}
