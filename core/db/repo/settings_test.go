package repo_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

func TestSettings_GetUpdateValidateTZ(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	cur, err := s.GetSettings(ctx)
	if err != nil || cur.ServerName != "Tollgate" || cur.Timezone != "UTC" {
		t.Fatalf("defaults: %+v %v", cur, err)
	}
	cur.ServerName, cur.Timezone = "Kyle GW", "Asia/Taipei"
	got, err := s.UpdateSettings(ctx, *cur)
	if err != nil || got.ServerName != "Kyle GW" || got.Timezone != "Asia/Taipei" {
		t.Fatalf("update: %+v %v", got, err)
	}
	cur.Timezone = "Mars/Olympus"
	if _, err := s.UpdateSettings(ctx, *cur); err == nil {
		t.Fatal("invalid timezone accepted")
	}
	if cur.LandingHeadline != "" || cur.LandingTagline != "" {
		t.Fatalf("landing defaults: %+v", cur)
	}
	cur.Timezone, cur.LandingHeadline, cur.LandingTagline = "UTC", "Hello", "World"
	if got, err := s.UpdateSettings(ctx, *cur); err != nil || got.LandingHeadline != "Hello" || got.LandingTagline != "World" {
		t.Fatalf("landing update: %+v %v", got, err)
	}
	cur.LandingHeadline = strings.Repeat("x", 81) // the DB CHECK backs up the handler's limit
	if _, err := s.UpdateSettings(ctx, *cur); err == nil {
		t.Fatal("81-char headline stored")
	}
}

// tzFixture is one key and one group, both daily, each carrying spend in the
// window that is currently open, so a timezone change has something to
// preserve AND something to move.
type tzFixture struct {
	s     *repo.Store
	group *repo.Group
	key   *repo.APIKey
	model *repo.Model
}

func newTZFixture(t *testing.T, keyBudget, groupBudget string) tzFixture {
	t.Helper()
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	// The default group is unlimited; give it a real window to move.
	g, err := s.UpdateGroup(ctx, g.ID, repo.GroupInput{Name: g.Name, BudgetType: groupBudget, BudgetNanos: 5_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	_, k, err := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: keyBudget, BudgetNanos: 1_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateModel(ctx, repo.ModelInput{Name: "tzm", Provider: "openai", UpstreamModel: "tzm", Enabled: true, Extra: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return tzFixture{s, g, k, m}
}

// seedSpend charges cost to both the fixture key and its group through the real
// spend path, so spend_nanos ends up exactly as RecordSpend left it.
func seedSpend(t *testing.T, f tzFixture, cost int64) {
	t.Helper()
	u, err := f.s.GetUser(context.Background(), f.key.UserID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.RecordSpend(context.Background(), []repo.SpendRecord{{
		KeyID: f.key.ID, UserID: u.ID, GroupID: f.group.ID, ModelID: f.model.ID, ModelName: f.model.Name,
		Provider: "openai", InputTokens: 10, OutputTokens: 5, CostNanos: cost,
		Status: "success", HTTPStatus: 200, CreatedAt: time.Now(),
	}})
	if err != nil {
		t.Fatal(err)
	}
}

// nextMidnight returns the next midnight in loc, i.e. what
// next_budget_reset('daily') must produce for that timezone.
func nextMidnight(t *testing.T, loc *time.Location, at time.Time) time.Time {
	t.Helper()
	local := at.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
}

// setTimezone is the act under test: read the settings, change only the zone
// (so the row is a full valid Settings value), and save.
func setTimezone(t *testing.T, s *repo.Store, zone string) *repo.Settings {
	t.Helper()
	ctx := context.Background()
	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.Timezone = zone
	got, err := s.UpdateSettings(ctx, *st)
	if err != nil {
		t.Fatalf("UpdateSettings(%s): %v", zone, err)
	}
	return got
}

// TestUpdateSettings_ReschedulesBudgetsInNewTimezone is the #27 core test.
//
// The fixture starts in UTC, so both budgets already have a budget_reset_at
// pinned to UTC midnight. After saving Asia/Taipei, both must land on the NEXT
// TAIPEI midnight (UTC+8), and the current-window spend must be preserved.
//
// This test is deliberately built so a stale-snapshot implementation CANNOT
// pass it. UTC midnight is 08:00 Taipei, so the two candidate instants are
// always 8 hours apart and neither can be mistaken for the other:
//
//   - it asserts the exact Taipei midnight instant, not merely "hour()==0";
//   - it additionally asserts the instant is NOT the UTC midnight the pre-update
//     snapshot would have produced, so even a coincidental match fails loudly.
//
// next_budget_reset() is STABLE, so this is the regression guard for the
// snapshot question the fix depends on (see the comment on UpdateSettings).
func TestUpdateSettings_ReschedulesBudgetsInNewTimezone(t *testing.T) {
	f := newTZFixture(t, "daily", "daily")
	ctx := context.Background()

	// Seed spend in the open window via the real spend path, so the counter is
	// exactly what RecordSpend produced.
	seedSpend(t, f, 240_000_000)

	before := time.Now()
	utc, err := time.LoadLocation("UTC")
	if err != nil {
		t.Fatal(err)
	}
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}

	// Precondition: the fixture's windows are UTC-anchored before the change.
	k0, err := f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k0.BudgetResetAt == nil {
		t.Fatal("precondition: a daily key must have a non-NULL budget_reset_at")
	}
	if !k0.BudgetResetAt.Equal(nextMidnight(t, utc, before)) && !k0.BudgetResetAt.Equal(nextMidnight(t, utc, before.Add(-time.Minute))) {
		t.Fatalf("precondition: key reset_at=%v, want the next UTC midnight", k0.BudgetResetAt)
	}

	setTimezone(t, f.s, "Asia/Taipei")
	after := time.Now()

	k, err := f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.s.GetGroup(ctx, f.group.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The two instants the buggy (stale-snapshot) implementation would produce.
	staleKey := *k0.BudgetResetAt
	staleGroup := nextMidnight(t, utc, after)

	wantKey := nextMidnight(t, taipei, before)
	if !k.BudgetResetAt.Equal(wantKey) && !k.BudgetResetAt.Equal(nextMidnight(t, taipei, after)) {
		t.Fatalf("key budget_reset_at = %v (Taipei local %v), want the next Asia/Taipei midnight %v",
			k.BudgetResetAt, k.BudgetResetAt.In(taipei), wantKey)
	}
	if k.BudgetResetAt.In(taipei).Hour() != 0 || k.BudgetResetAt.In(taipei).Minute() != 0 || k.BudgetResetAt.In(taipei).Second() != 0 {
		t.Fatalf("key budget_reset_at local %v is not Taipei midnight", k.BudgetResetAt.In(taipei))
	}
	if k.BudgetResetAt.Equal(staleKey) {
		t.Fatalf("key budget_reset_at %v is the PRE-update UTC midnight: the recompute saw the old snapshot", k.BudgetResetAt)
	}
	if k.BudgetResetAt.In(utc).Hour() == 0 && k.BudgetResetAt.In(utc).Minute() == 0 {
		t.Fatalf("key budget_reset_at %v is still a UTC midnight, not a Taipei one", k.BudgetResetAt)
	}

	wantGroup := nextMidnight(t, taipei, before)
	if !g.BudgetResetAt.Equal(wantGroup) && !g.BudgetResetAt.Equal(nextMidnight(t, taipei, after)) {
		t.Fatalf("group budget_reset_at = %v (Taipei local %v), want the next Asia/Taipei midnight %v",
			g.BudgetResetAt, g.BudgetResetAt.In(taipei), wantGroup)
	}
	if g.BudgetResetAt.Equal(staleGroup) || g.BudgetResetAt.Equal(staleKey) {
		t.Fatalf("group budget_reset_at %v is the PRE-update UTC midnight: the recompute saw the old snapshot", g.BudgetResetAt)
	}
}

// TestUpdateSettings_PreservesSpendAndLeavesNonWindowedBudgets pins the two
// blast-radius limits of the reschedule: it moves ONLY the reset instant, and
// it touches ONLY daily/monthly rows.
func TestUpdateSettings_PreservesSpendAndLeavesNonWindowedBudgets(t *testing.T) {
	f := newTZFixture(t, "daily", "daily")
	ctx := context.Background()

	// One of each non-windowed type: 'total' has a cap but no reset, and
	// 'unlimited' has neither.
	u, err := f.s.GetUser(ctx, f.key.UserID)
	if err != nil {
		t.Fatal(err)
	}
	_, totalKey, err := f.s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "total", GroupID: f.group.ID, BudgetType: "total", BudgetNanos: 1_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	_, unlimKey, err := f.s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "unlim", GroupID: f.group.ID, BudgetType: "unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	totalGroup, err := f.s.CreateGroup(ctx, repo.GroupInput{Name: "totals", BudgetType: "total", BudgetNanos: 2_000_000_000})
	if err != nil {
		t.Fatal(err)
	}

	// Charge both windowed scopes with the real spend path.
	seedSpend(t, f, 150_000_000)
	spendNanos := int64(150_000_000)

	// Precondition: non-windowed budgets have no reset instant to move.
	for _, id := range []string{totalKey.ID, unlimKey.ID} {
		if n, err := f.s.QueryForTest(ctx, `SELECT budget_reset_at IS NULL FROM api_keys WHERE id=$1`, id); err != nil || n != true {
			t.Fatalf("precondition: key %s budget_reset_at IS NULL = %v, %v", id, n, err)
		}
	}
	if n, err := f.s.QueryForTest(ctx, `SELECT budget_reset_at IS NULL FROM groups WHERE id=$1`, totalGroup.ID); err != nil || n != true {
		t.Fatalf("precondition: total group budget_reset_at IS NULL = %v, %v", n, err)
	}

	setTimezone(t, f.s, "Asia/Taipei")

	k, err := f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.s.GetGroup(ctx, f.group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k.SpendNanos != spendNanos {
		t.Fatalf("key spend_nanos = %d, want %d preserved (only the reset instant moves)", k.SpendNanos, spendNanos)
	}
	if g.SpendNanos != spendNanos {
		t.Fatalf("group spend_nanos = %d, want %d preserved (only the reset instant moves)", g.SpendNanos, spendNanos)
	}

	// 'total' and 'unlimited' must still have NULL resets.
	for _, id := range []string{totalKey.ID, unlimKey.ID} {
		if n, err := f.s.QueryForTest(ctx, `SELECT budget_reset_at IS NULL FROM api_keys WHERE id=$1`, id); err != nil || n != true {
			t.Fatalf("key %s budget_reset_at IS NULL = %v after the change, want true (non-windowed must be untouched)", id, n)
		}
	}
	tg, err := f.s.GetGroup(ctx, totalGroup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tg.BudgetResetAt != nil {
		t.Fatalf("total group budget_reset_at = %v, want NULL (non-windowed must be untouched)", tg.BudgetResetAt)
	}
}

// TestUpdateSettings_ReschedulesMonthlyBudgets covers the monthly window, whose
// reset is a month boundary rather than a day one — the recompute must pick up
// the new zone for it too.
func TestUpdateSettings_ReschedulesMonthlyBudgets(t *testing.T) {
	f := newTZFixture(t, "monthly", "monthly")
	ctx := context.Background()

	setTimezone(t, f.s, "Asia/Taipei")
	after := time.Now()
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}
	wantLocal := time.Date(after.In(taipei).Year(), after.In(taipei).Month(), 1, 0, 0, 0, 0, taipei).AddDate(0, 1, 0)

	k, err := f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.s.GetGroup(ctx, f.group.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]time.Time{"key": *k.BudgetResetAt, "group": *g.BudgetResetAt} {
		local := got.In(taipei)
		if local.Day() != 1 || local.Hour() != 0 || local.Minute() != 0 {
			t.Fatalf("%s monthly reset = %v (Taipei local %v), want the 1st of the month at Taipei midnight", name, got, local)
		}
		if !got.Equal(wantLocal) && !got.Equal(wantLocal.AddDate(0, 1, 0)) {
			t.Fatalf("%s monthly reset = %v, want %v", name, got, wantLocal)
		}
	}
}

// TestUpdateSettings_DoesNotForgiveAnOverdueWindow pins the boundary with
// ResetBudgets: a window whose reset instant has already PASSED must not be
// rescheduled behind ResetBudgets' back.
//
// The hazard the gate prevents is NOT forgiveness — it is the opposite. Limits
// .EffectiveSpend returns 0 for an overdue window (#21), so the stale counter
// is currently disregarded. Moving the reset into the future WITHOUT zeroing
// spend makes the row look live again, EffectiveSpend starts returning that
// stale counter, and the scope reads OverBudget until the worker runs:
//
//	overdue, spend 1.5B / cap 1B -> EffectiveSpend 0    -> not blocked
//	reset pushed forward         -> EffectiveSpend 1.5B -> spurious block
//
// The fixture spend MUST exceed the cap OF THAT SCOPE for this to bite: a
// resurrected counter that still sits below its cap leaves the assertion
// vacuously true. That mistake was made twice here — first on the key (900M of
// 1B), then on the GROUP, whose cap is 5B while the test only charged 1.5B. So
// this test lowers the group's cap rather than reusing the key's number: the
// two scopes have different budgets, and both must be genuinely over-cap.
// Do not "tidy" either number back down.
//
// So this asserts both halves: the reset stays in the past, AND the scope is
// still not reported over budget in the meantime.
func TestUpdateSettings_DoesNotForgiveAnOverdueWindow(t *testing.T) {
	f := newTZFixture(t, "daily", "daily")
	ctx := context.Background()

	// Give the group the same 1B cap as the key so 1.5B of spend is over-cap
	// for both. (newTZFixture gives the group 5B.)
	if _, err := f.s.UpdateGroup(ctx, f.group.ID, repo.GroupInput{
		Name: f.group.Name, BudgetType: "daily", BudgetNanos: 1_000_000_000}); err != nil {
		t.Fatal(err)
	}

	// Make the windows overdue and over-cap without running the reset worker.
	if err := f.s.ExecForTest(ctx, `UPDATE api_keys SET spend_nanos=1500000000, budget_reset_at=now() - interval '1 hour' WHERE id=$1`, f.key.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ExecForTest(ctx, `UPDATE groups SET spend_nanos=1500000000, budget_reset_at=now() - interval '1 hour' WHERE id=$1`, f.group.ID); err != nil {
		t.Fatal(err)
	}

	// Precondition: the disregard is real — an overdue window never reports as
	// over budget, because EffectiveSpend collapses it to 0. Checked for BOTH
	// scopes, and each must genuinely be over-cap for this to mean anything.
	if k, err := f.s.GetKey(ctx, f.key.ID); err != nil {
		t.Fatal(err)
	} else if k.SpendNanos <= k.BudgetNanos {
		t.Fatalf("precondition: key spend %d must exceed its cap %d, else the assertion below is vacuous", k.SpendNanos, k.BudgetNanos)
	} else if k.OverBudget(time.Now()) {
		t.Fatal("precondition: an overdue window must not report OverBudget")
	}
	if g, err := f.s.GetGroup(ctx, f.group.ID); err != nil {
		t.Fatal(err)
	} else if g.SpendNanos <= g.BudgetNanos {
		t.Fatalf("precondition: group spend %d must exceed its cap %d, else the group assertion below is vacuous", g.SpendNanos, g.BudgetNanos)
	} else if g.OverBudget(time.Now()) {
		t.Fatal("precondition: an overdue group window must not report OverBudget")
	}

	setTimezone(t, f.s, "Asia/Taipei")

	k, err := f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !k.BudgetResetAt.Before(time.Now()) {
		t.Fatalf("key overdue reset_at = %v, want it left in the past for ResetBudgets to handle", k.BudgetResetAt)
	}
	if k.SpendNanos != 1_500_000_000 {
		t.Fatalf("key spend_nanos = %d, want 1500000000 untouched", k.SpendNanos)
	}
	// The hazard itself: the gate must not have resurrected the stale counter.
	if k.OverBudget(time.Now()) {
		t.Fatalf("key reads OverBudget (effective spend %d of %d) after a settings save: the gate let an overdue window be re-activated",
			k.EffectiveSpend(time.Now()), k.BudgetNanos)
	}
	g, err := f.s.GetGroup(ctx, f.group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.OverBudget(time.Now()) {
		t.Fatalf("group reads OverBudget (effective spend %d of %d) after a settings save: the gate let an overdue window be re-activated",
			g.EffectiveSpend(time.Now()), g.BudgetNanos)
	}

	// The reset worker still does its job afterwards.
	if _, err := f.s.ResetBudgets(ctx); err != nil {
		t.Fatal(err)
	}
	k, err = f.s.GetKey(ctx, f.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if k.SpendNanos != 0 || !k.BudgetResetAt.After(time.Now()) {
		t.Fatalf("after ResetBudgets: spend=%d reset_at=%v, want 0 and a future instant", k.SpendNanos, k.BudgetResetAt)
	}
}

// TestUpdateSettings_RecomputeSeesNewTimezoneDirectly pins next_budget_reset()'s
// POST-COMMIT behaviour: after a settings save commits, calling the function on a
// fresh connection returns the new zone's midnight.
//
// Scope, stated precisely because an earlier version of this comment overstated
// it: the read runs on s.pool via QueryForTest, i.e. a SEPARATE connection
// OUTSIDE the UpdateSettings transaction, and only after Commit. So it observes
// committed state and says NOTHING about what the recompute saw inside the tx.
// Falsified by mutation: deleting the whole in-transaction reschedule loop
// leaves this test passing while the two reschedule tests fail.
//
// It is kept as a cheap guard on the function's own contract (the SQL formula is
// the one ResetBudgets and rollSpend share too). The real STABLE-snapshot guard
// is TestUpdateSettings_ReschedulesBudgetsInNewTimezone, which does its assertion
// against rows the transaction actually rewrote.
func TestUpdateSettings_RecomputeSeesNewTimezoneDirectly(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatal(err)
	}

	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.Timezone = "Asia/Taipei"
	if _, err := s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	got, err := s.QueryForTest(ctx, `SELECT next_budget_reset('daily')`)
	if err != nil {
		t.Fatal(err)
	}
	reset, ok := got.(time.Time)
	if !ok {
		t.Fatalf("next_budget_reset type = %T, want time.Time", got)
	}
	local := reset.In(taipei)
	if local.Hour() != 0 || local.Minute() != 0 {
		t.Fatalf("next_budget_reset('daily') = %v (Taipei local %v), want Taipei midnight", reset, local)
	}
	if !reset.Equal(nextMidnight(t, taipei, now)) && !reset.Equal(nextMidnight(t, taipei, now.Add(-time.Minute))) {
		t.Fatalf("next_budget_reset('daily') = %v, want %v", reset, nextMidnight(t, taipei, now))
	}
}
