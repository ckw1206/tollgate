package consoleapi

import (
	"context"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

type gateResp struct {
	ProvidersUp    int    `json:"providers_up"`
	ProvidersTotal int    `json:"providers_total"`
	RPM            *int64 `json:"rpm"`
	P50MS          *int64 `json:"p50_ms"`
	Budget         *struct {
		Type        string     `json:"type"`
		BudgetNanos int64      `json:"budget_nanos"`
		SpendNanos  int64      `json:"spend_nanos"`
		ResetAt     *time.Time `json:"reset_at"`
	} `json:"budget"`
}

func TestGate_RpmOnlyForReadAll(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()

	// No spend yet: no latency rows (p50 null), no recent requests (rpm 0),
	// and the default group is unlimited (budget null).
	empty := decodeInto[gateResp](t, e.as("admin@x.io", "GET", p+"/gate", nil))
	if empty.P50MS != nil || empty.RPM == nil || *empty.RPM != 0 || empty.Budget != nil {
		t.Fatalf("empty: %+v", empty)
	}
	if empty.ProvidersTotal != 1 || empty.ProvidersUp != 1 {
		t.Fatalf("providers: up=%d total=%d, want 1/1", empty.ProvidersUp, empty.ProvidersTotal)
	}

	// The member's own group: a daily $5 budget with $4.30 already spent.
	if _, err := e.store.UpdateGroup(ctx, e.group.ID, repo.GroupInput{Name: "default", BudgetType: "daily", BudgetNanos: 5_000_000_000}); err != nil {
		t.Fatal(err)
	}
	fast := modelByName(t, e, "fast")
	if _, err := e.store.RecordSpend(ctx, []repo.SpendRecord{{
		UserID: e.member.ID, GroupID: e.group.ID, ModelID: fast.ID, ModelName: "fast", Provider: "openai",
		CostNanos: 4_300_000_000, Status: "success", HTTPStatus: 200, CreatedAt: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}

	// Admin (spend:read_all): rpm is a number; budget reflects own group.
	got := decodeInto[gateResp](t, e.as("admin@x.io", "GET", p+"/gate", nil))
	if got.RPM == nil || *got.RPM < 1 {
		t.Fatalf("admin rpm=%v, want a number >= 1", got.RPM)
	}
	if got.P50MS == nil {
		t.Fatalf("admin p50_ms null with a spend row present")
	}
	if got.Budget == nil || got.Budget.Type != "daily" || got.Budget.BudgetNanos != 5_000_000_000 ||
		got.Budget.SpendNanos != 4_300_000_000 || got.Budget.ResetAt == nil {
		t.Fatalf("admin budget=%+v", got.Budget)
	}

	// Member (spend:read_own only): rpm is null; budget still reflects own group.
	got = decodeInto[gateResp](t, e.as("member@x.io", "GET", p+"/gate", nil))
	if got.RPM != nil {
		t.Fatalf("member rpm=%v, want null", got.RPM)
	}
	if got.Budget == nil || got.Budget.Type != "daily" || got.Budget.BudgetNanos != 5_000_000_000 ||
		got.Budget.SpendNanos != 4_300_000_000 || got.Budget.ResetAt == nil {
		t.Fatalf("member budget=%+v", got.Budget)
	}

	// An unlimited group has nothing to meter: budget is null.
	if _, err := e.store.UpdateGroup(ctx, e.group.ID, repo.GroupInput{Name: "default", BudgetType: "unlimited"}); err != nil {
		t.Fatal(err)
	}
	if got = decodeInto[gateResp](t, e.as("member@x.io", "GET", p+"/gate", nil)); got.Budget != nil {
		t.Fatalf("unlimited budget=%+v, want null", got.Budget)
	}
}

// TestGate_ProviderDownRule: a provider is down when it has >= 3 requests in
// the past 5 minutes and all of its latest three are 5xx (Decision 4).
func TestGate_ProviderDownRule(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()
	fast := modelByName(t, e, "fast")
	now := time.Now()
	rec := func(at time.Time, status int) {
		t.Helper()
		if _, err := e.store.RecordSpend(ctx, []repo.SpendRecord{{
			UserID: e.member.ID, GroupID: e.group.ID, ModelID: fast.ID, ModelName: "fast", Provider: "openai",
			Status: "error", HTTPStatus: status, CreatedAt: at,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	up := func() int {
		t.Helper()
		return decodeInto[gateResp](t, e.as("admin@x.io", "GET", p+"/gate", nil)).ProvidersUp
	}

	// Two 5xx rows: under the three-request threshold, still up.
	rec(now.Add(-time.Minute), 500)
	rec(now.Add(-30*time.Second), 502)
	if got := up(); got != 1 {
		t.Fatalf("2 x 5xx: up=%d, want 1", got)
	}
	// A third 5xx: down.
	rec(now.Add(-10*time.Second), 503)
	if got := up(); got != 0 {
		t.Fatalf("3 x 5xx: up=%d, want 0", got)
	}
	// A recent success lands in the latest three: back up.
	if _, err := e.store.RecordSpend(ctx, []repo.SpendRecord{{
		UserID: e.member.ID, GroupID: e.group.ID, ModelID: fast.ID, ModelName: "fast", Provider: "openai",
		Status: "success", HTTPStatus: 200, CreatedAt: now.Add(-2 * time.Second),
	}}); err != nil {
		t.Fatal(err)
	}
	if got := up(); got != 1 {
		t.Fatalf("recent 200: up=%d, want 1", got)
	}
}
