package repo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

type fixture struct {
	s     *repo.Store
	g     *repo.Group
	u     *repo.User
	k     *repo.APIKey
	model *repo.Model
}

func newFixture(t *testing.T, keyBudget string) fixture {
	s, g := newStore(t)
	ctx := context.Background()
	u := admin(t, s, g)
	_, k, err := s.CreateKey(ctx, u.ID, repo.KeyInput{Alias: "k", GroupID: g.ID, BudgetType: keyBudget, BudgetNanos: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateModel(ctx, repo.ModelInput{Name: "m1", Provider: "openai", UpstreamModel: "m1", Enabled: true, Extra: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{s, g, u, k, m}
}

func (f fixture) rec(cost int64, at time.Time) repo.SpendRecord {
	return repo.SpendRecord{KeyID: f.k.ID, UserID: f.u.ID, GroupID: f.g.ID, ModelID: f.model.ID, ModelName: "m1",
		Provider: "openai", InputTokens: 10, OutputTokens: 5, CostNanos: cost, Status: "success", HTTPStatus: 200, CreatedAt: at}
}

func TestRecordSpend_IncrementsCountersAndLogs(t *testing.T) {
	f := newFixture(t, "monthly")
	ctx := context.Background()
	now := time.Now()
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, now), f.rec(250, now)}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	g, _ := f.s.GetGroup(ctx, f.g.ID)
	if k.SpendNanos != 350 || g.SpendNanos != 350 || k.LastUsedAt == nil {
		t.Fatalf("key=%d group=%d", k.SpendNanos, g.SpendNanos)
	}
	rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), GroupBy: "model"})
	if err != nil || len(rows) != 1 || rows[0].Requests != 2 || rows[0].CostNanos != 350 || rows[0].Bucket != "m1" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestRecordSpend_OldWindowRecordIsLoggedNotCharged(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()
	old := now.AddDate(0, 0, -1)
	// One record in the previous window, one in the current window.
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, old), f.rec(250, now)}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	// Only the current-window record (250) is charged to spend_nanos.
	if k.SpendNanos != 250 {
		t.Fatalf("spend_nanos=%d, want 250 (only in-window charged)", k.SpendNanos)
	}
	// Both records are still logged.
	rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{From: now.Add(-48 * time.Hour), To: now.Add(time.Hour), GroupBy: "model"})
	if err != nil || len(rows) != 1 || rows[0].Requests != 2 {
		t.Fatalf("rows=%+v err=%v, want 2 logged records", rows, err)
	}
}

func TestRecordSpend_InWindowRecordCharged(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(350, now)}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 350 {
		t.Fatalf("spend_nanos=%d, want 350", k.SpendNanos)
	}
}

func TestRecordSpend_TotalBudgetUnaffected(t *testing.T) {
	f := newFixture(t, "total")
	ctx := context.Background()
	// total has no window, so even an old record is charged.
	old := time.Now().AddDate(0, 0, -1)
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(400, old)}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 400 {
		t.Fatalf("spend_nanos=%d, want 400 (total budget charges all)", k.SpendNanos)
	}
}

func TestRecordSpend_KeylessPlaygroundRow(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	now := time.Now()
	pg := f.rec(5000, now)
	pg.KeyID, pg.Source = "", "playground"
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{pg, f.rec(7000, now)}); err != nil {
		t.Fatal(err)
	}
	g, _ := f.s.GetGroup(ctx, f.g.ID)
	if g.SpendNanos != 12000 {
		t.Fatalf("group spend_nanos=%d, want 12000 (keyless rows still charge the group)", g.SpendNanos)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 7000 {
		t.Fatalf("key spend_nanos=%d, want 7000 (a keyless row must not charge a key)", k.SpendNanos)
	}
	n, err := f.s.QueryForTest(ctx, `SELECT count(*) FROM spend_logs WHERE key_id IS NULL AND source = 'playground'`)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := n.(int64); c != 1 {
		t.Fatalf("keyless playground rows = %d, want 1", c)
	}
	n, err = f.s.QueryForTest(ctx, `SELECT count(*) FROM spend_logs WHERE key_id = $1 AND source = 'api'`, f.k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := n.(int64); c != 1 {
		t.Fatalf("keyed api-source rows = %d, want 1 (default source must be 'api')", c)
	}
}

func TestRecentSpend_IncludesPlayground(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	now := time.Now()
	pg := f.rec(5000, now)
	pg.KeyID, pg.Source = "", "playground"
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(7000, now), pg}); err != nil {
		t.Fatal(err)
	}
	logs, err := f.s.RecentSpend(ctx, repo.SpendFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour)}, 10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("logs=%+v err=%v, want 2 rows", logs, err)
	}
	var pgFound, apiFound bool
	for _, l := range logs {
		switch l.Source {
		case "playground":
			pgFound = true
			if l.KeyID != "" || l.KeyAlias != "Playground" || l.KeyPrefix != "session" || l.OwnerEmail != f.u.Email {
				t.Fatalf("playground row = %+v, want keyless Playground/session row owned by %s", l, f.u.Email)
			}
		case "api":
			apiFound = true
			if l.KeyID != f.k.ID || l.KeyAlias != "k" {
				t.Fatalf("api row = %+v, want the fixture key's alias", l)
			}
		default:
			t.Fatalf("row with missing source: %+v", l)
		}
	}
	if !pgFound || !apiFound {
		t.Fatalf("pgFound=%v apiFound=%v, want both", pgFound, apiFound)
	}
}

func TestQuerySpend_KeyBucketCoalescesPlayground(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	now := time.Now()
	pg := f.rec(5000, now)
	pg.KeyID, pg.Source = "", "playground"
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(7000, now), pg}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), GroupBy: "key"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v, want 2 key buckets", rows, err)
	}
	var pgBucket, keyBucket *repo.SpendRow
	for i := range rows {
		switch rows[i].Bucket {
		case "playground":
			pgBucket = &rows[i]
		case f.k.ID:
			keyBucket = &rows[i]
		default:
			t.Fatalf("unexpected bucket %q", rows[i].Bucket)
		}
	}
	if pgBucket == nil || keyBucket == nil {
		t.Fatalf("buckets = %+v, want one 'playground' and one keyed", rows)
	}
	if pgBucket.Requests != 1 || pgBucket.CostNanos != 5000 {
		t.Fatalf("playground bucket = %+v, want 1 request / 5000 nanos", pgBucket)
	}
}

func TestQuerySpend_ModelDayInServerTimezone(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei" // UTC+8: 20:00Z is already the next day
	f.s.UpdateSettings(ctx, *st)
	m2, _ := f.s.CreateModel(ctx, repo.ModelInput{Name: "m2", Provider: "openai", UpstreamModel: "m2", Enabled: true, Extra: json.RawMessage(`{}`)})
	d1 := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	other := func(cost int64, at time.Time) repo.SpendRecord {
		r := f.rec(cost, at)
		r.ModelID, r.ModelName = m2.ID, "m2"
		return r
	}
	f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, d1), f.rec(50, d1), f.rec(200, d2), other(300, d1), other(400, d2)})
	rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{From: d1.Add(-time.Hour), To: d2.Add(time.Hour), GroupBy: "model,day"})
	want := []repo.SpendRow{
		{Bucket: "m1", Day: "2026-01-02", Requests: 2, InputTokens: 20, OutputTokens: 10, CostNanos: 150},
		{Bucket: "m2", Day: "2026-01-02", Requests: 1, InputTokens: 10, OutputTokens: 5, CostNanos: 300},
		{Bucket: "m1", Day: "2026-01-03", Requests: 1, InputTokens: 10, OutputTokens: 5, CostNanos: 200},
		{Bucket: "m2", Day: "2026-01-03", Requests: 1, InputTokens: 10, OutputTokens: 5, CostNanos: 400},
	}
	if err != nil || len(rows) != len(want) {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if _, err := f.s.QuerySpend(ctx, repo.SpendFilter{GroupBy: "hourly"}); err != repo.ErrGroupBy {
		t.Fatalf("bad group_by: %v", err)
	}
}

func TestResetBudgets_OnlyDueWindows(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(500, time.Now())})
	if n, _ := f.s.ResetBudgets(ctx); n != 0 {
		t.Fatalf("reset %d rows before due", n)
	}
	forceDue(t, f)
	if _, err := f.s.ResetBudgets(ctx); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 0 || !k.BudgetResetAt.After(time.Now()) {
		t.Fatalf("spend=%d reset_at=%v", k.SpendNanos, k.BudgetResetAt)
	}
}

func TestResetBudgets_UsesServerTimezone(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei"
	f.s.UpdateSettings(ctx, *st)
	forceDue(t, f)
	f.s.ResetBudgets(ctx)
	k, _ := f.s.GetKey(ctx, f.k.ID)
	tpe, _ := time.LoadLocation("Asia/Taipei")
	local := k.BudgetResetAt.In(tpe)
	if local.Hour() != 0 || local.Minute() != 0 {
		t.Fatalf("reset at %v, want local midnight in Asia/Taipei", local)
	}
}

func forceDue(t *testing.T, f fixture) {
	t.Helper()
	if err := f.s.ExecForTest(context.Background(), `UPDATE api_keys SET budget_reset_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
}

// crossingWindowStart returns the fixture key's current window start, mirroring
// WindowStart's rule: a windowed type's active window starts one period before
// its reset instant (derived in the SERVER's timezone), an expired one (reset
// in the past) starts at the reset instant; total/unlimited use the fixed
// epoch sentinel.
func crossingWindowStart(t *testing.T, f fixture) time.Time {
	t.Helper()
	k, err := f.s.GetKey(context.Background(), f.k.ID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := f.s.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		t.Fatal(err)
	}
	ws := k.Limits.WindowStart(time.Now(), loc)
	if ws == nil {
		t.Fatal("window start is nil")
	}
	return *ws
}

// forceWindowStart moves the fixture key's window to the one whose reset instant
// is `at`, zeroing spend as the reset worker would. Pushing `at` into the past
// makes WindowStart(now) return a DISTINCT window start, simulating a midnight
// rollover that the unit test can't produce from the wall clock.
func forceWindowStart(t *testing.T, f fixture, at time.Time) {
	t.Helper()
	if err := f.s.ExecForTest(context.Background(), `UPDATE api_keys SET spend_nanos = 0, budget_reset_at = $1 WHERE id = $2`, at, f.k.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRecordSpend_Crosses80OnceThen100Once(t *testing.T) {
	f := newFixture(t, "daily") // budget_nanos = 1e9: 80% = 800M, 100% = 1000M
	ctx := context.Background()
	now := time.Now()

	// 0 -> 900M: crosses 80% only.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 1 || cx[0].Threshold != 80 || cx[0].Scope != "key" || cx[0].Label != "k" {
		t.Fatalf("batch1: got %+v, want one 80 key crossing (label k)", cx)
	}
	if cx[0].SpendNanos != 900_000_000 || cx[0].BudgetNanos != 1_000_000_000 {
		t.Fatalf("batch1 spend/budget: %+v", cx[0])
	}

	// 900M -> 1050M: crosses 100% only (80% already notified this window).
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(150_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 1 || cx[0].Threshold != 100 || cx[0].SpendNanos != 1_050_000_000 {
		t.Fatalf("batch2: got %+v, want one 100 crossing at 1050M", cx)
	}

	// Same window, already at/over both thresholds: no new crossings.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(10_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("batch3: got %+v, want zero crossings", cx)
	}
}

func TestRecordSpend_CrossingFiresAgainAfterReset(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()

	// Window A: 0 -> 900M crosses 80% once; the marker now holds wsA at pct 80.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil || len(cx) != 1 || cx[0].Threshold != 80 {
		t.Fatalf("window A: cx=%+v err=%v, want one 80 crossing", cx, err)
	}
	wsA := crossingWindowStart(t, f)

	// Force the window due and run the real reset. This zeroes spend and
	// schedules the next window but does NOT touch the notify marker (ResetBudgets
	// must not clear it). Because the wall clock can't move past midnight in a unit
	// test, next_budget_reset lands on the same next-midnight, so the window start
	// is unchanged: the marker still holds wsA and must block a re-fire.
	forceDue(t, f)
	if _, err := f.s.ResetBudgets(ctx); err != nil {
		t.Fatal(err)
	}
	wsAfterReset := crossingWindowStart(t, f)
	if !wsAfterReset.Equal(wsA) {
		t.Fatalf("reset did not advance the window in a unit test: wsA=%v wsAfter=%v", wsA, wsAfterReset)
	}
	// Negative control: same window, spend re-crosses 80% -> the marker holds and
	// NO crossing fires (this is what marker clearing in ResetBudgets would break).
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("same window after reset: got %+v, want zero crossings (marker still holds %v)", cx, wsA)
	}

	// Advance the window to a DISTINCT start (simulate the midnight rollover): a
	// fresh record now lands in window B. The marker UPDATE's IS DISTINCT FROM $ws
	// clause fires because wsB != wsA — re-arm via the changed window start, with
	// no marker clearing.
	forceWindowStart(t, f, now.AddDate(0, 0, -1)) // reset 1s ago: expired, wsB = 1 day before now
	wsB := crossingWindowStart(t, f)
	if wsB.Equal(wsA) {
		t.Fatalf("forced wsB %v equals wsA %v; the re-fire below would be unproven", wsB, wsA)
	}
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 1 || cx[0].Threshold != 80 || cx[0].SpendNanos != 900_000_000 {
		t.Fatalf("window B: got %+v, want one fresh 80 crossing at 900M (re-armed by the new ws)", cx)
	}
}

// have reports whether cx contains a crossing for the given scope at the
// given threshold. Crossing order within a batch is not guaranteed, so the
// tests assert membership rather than position.
func have(cx []repo.BudgetCrossing, scope string, thr int) bool {
	for _, c := range cx {
		if c.Scope == scope && c.Threshold == thr {
			return true
		}
	}
	return false
}

// TestRecordSpend_MaxedKeyRefiresBothThresholdsNextWindow guards the case the
// "fires again after reset" test can't reach: a key that maxed out (100%) in a
// window and does so again. Because budget_notified_pct is stored per-window
// and only re-armed when the window start changes, a chronically-maxed key must
// still fire BOTH 80% and 100% in the next window. Before the per-window pct
// fix, the marker's pct was monotonic (GREATEST, never reset), so once it was
// stuck at 100 the next window's 80% re-armed the window but left pct at 100,
// and the 100% guard (window IS DISTINCT FROM ws OR pct < 100) evaluated false
// — the 100% crossing was silently dropped.
func TestRecordSpend_MaxedKeyRefiresBothThresholdsNextWindow(t *testing.T) {
	f := newFixture(t, "daily") // budget_nanos = 1e9: 80% = 800M, 100% = 1000M
	ctx := context.Background()
	now := time.Now()

	// Window A: 0 -> 1050M crosses BOTH 80% and 100% in a single batch. After
	// this the notify marker holds wsA with pct 100 (the key is chronically
	// maxed).
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) || !have(cx, "key", 100) {
		t.Fatalf("window A: got %+v, want both 80 and 100 key crossings", cx)
	}
	wsA := crossingWindowStart(t, f)

	// Advance the window to a DISTINCT start (simulate the midnight rollover)
	// WITHOUT running ResetBudgets: the marker is not cleared, so it still says
	// wsA at pct 100. This is exactly the setup where a monotonic pct would
	// drop the next window's 100%.
	forceWindowStart(t, f, now.AddDate(0, 0, -1)) // reset 1s ago: expired, wsB = 1 day before now
	wsB := crossingWindowStart(t, f)
	if wsB.Equal(wsA) {
		t.Fatalf("forced wsB %v equals wsA %v; the re-fire below would be unproven", wsB, wsA)
	}

	// Window B: fresh in-window spend 0 -> 1050M must cross BOTH thresholds
	// again. Before the fix the marker re-arms at 80% (window now wsB) but pct
	// stays stuck at 100, so the 100% guard is false and the 100% crossing is
	// silently dropped; after the per-window fix both fire.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) {
		t.Fatalf("window B: got %+v, want the 80 key crossing to re-fire", cx)
	}
	if !have(cx, "key", 100) {
		t.Fatalf("window B: got %+v, want the 100 key crossing to re-fire (a chronic 100%% must not suppress the next window's 100%%)", cx)
	}
}

func TestRecordSpend_CrossingFiresInExpiredWindowBeforeReset(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()
	// Fill the current window to the cap.
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_000_000_000, now)}); err != nil {
		t.Fatal(err)
	}
	// Expire the window (worker not yet run): raw spend_nanos is still 1000M,
	// but the new window's effective spend is 0.
	forceDue(t, f)
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 1 || cx[0].Threshold != 80 || cx[0].Scope != "key" {
		t.Fatalf("expired window: got %+v, want one 80 key crossing", cx)
	}
	// SpendNanos reflects only the new window (900M), not the stale 1000M.
	if cx[0].SpendNanos != 900_000_000 {
		t.Fatalf("SpendNanos=%d, want 900M (not the stale 1000M)", cx[0].SpendNanos)
	}
}

// TestRecordSpend_TotalBudgetCrossesOnce guards the case the old
// "total never crosses" test locked in (A4): a key with a total budget has a
// fixed window identity (the epoch sentinel), so its 80%/100% crossings fire
// exactly once over its lifetime — callers already get 429 budget_exceeded at
// 100%, and the webhook must not be the only thing that stays silent.
func TestRecordSpend_TotalBudgetCrossesOnce(t *testing.T) {
	f := newFixture(t, "total")
	ctx := context.Background()
	now := time.Now()
	// 0 -> 900M: crosses 80% once (budget_nanos = 1e9).
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) {
		t.Fatalf("total budget: got %+v, want the 80 key crossing", cx)
	}
	// 900M -> 1050M: crosses 100% once.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(150_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 100) {
		t.Fatalf("total budget: got %+v, want the 100 key crossing", cx)
	}
	// The sentinel window never re-arms: further overage fires nothing.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(10_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("total budget: got %+v, want zero crossings (one-shot per lifetime)", cx)
	}
	// The total counter is the plain sum — no window expiry can apply.
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 1_060_000_000 {
		t.Fatalf("spend_nanos=%d, want 1.06B", k.SpendNanos)
	}
	// Unlimited budgets still have no threshold: no crossings ever.
	f2 := newFixture(t, "unlimited")
	cx, err = f2.s.RecordSpend(ctx, []repo.SpendRecord{f2.rec(5_000_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("unlimited budget: got %+v, want zero crossings", cx)
	}
}

func TestDeleteSpendBefore(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -100)
	f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1, old), f.rec(1, time.Now())})
	n, err := f.s.DeleteSpendBefore(ctx, time.Now().AddDate(0, 0, -90))
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
}

// forceWindowAt sets the fixture key's window to the one whose reset instant
// is `at` (freshly started: counter zero, marker cleared) without running the
// reset worker.
func forceWindowAt(t *testing.T, f fixture, at time.Time) {
	t.Helper()
	if err := f.s.ExecForTest(context.Background(),
		`UPDATE api_keys SET spend_nanos = 0, budget_reset_at = $1, budget_notified_window = NULL, budget_notified_pct = 0 WHERE id = $2`,
		at, f.k.ID); err != nil {
		t.Fatal(err)
	}
}

// TestRecordSpend_ExpiredWindowSpendChargedToNewWindow is the A3 scenario:
// the window's budget_reset_at has passed but the reset worker has not run
// yet. The spend logged in that gap must charge the NEW window — not vanish
// when the worker later zeroes the counter, and not pile onto the stale old
// counter — and its thresholds must still cross in the gap.
func TestRecordSpend_ExpiredWindowSpendChargedToNewWindow(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()

	// Fill the current window to the cap.
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_000_000_000, now)}); err != nil {
		t.Fatal(err)
	}
	// Expire the window without running the reset worker: the stored counter
	// is the stale 1B from the old window.
	forceDue(t, f)

	// A $900M spend in the gap (window due, worker not run). It must start
	// the fresh window from zero — the pre-fix code charged it ON TOP of the
	// stale counter, and ResetBudgets then erased it.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 900_000_000 {
		t.Fatalf("spend_nanos=%d, want 900M (new window only, not stale 1B + 900M)", k.SpendNanos)
	}
	// The inline roll rescheduled the window: it must be in the future again,
	// and the old window's identity is gone from the row.
	if k.BudgetResetAt == nil || !k.BudgetResetAt.After(now) {
		t.Fatalf("budget_reset_at=%v, want a fresh window in the future", k.BudgetResetAt)
	}
	// Crossing detection saw the POST-roll counter: the first write into the
	// fresh window crossed 80% (and not 100%).
	if !have(cx, "key", 80) || have(cx, "key", 100) {
		t.Fatalf("gap crossings: got %+v, want the 80 key crossing only", cx)
	}
	if len(cx) > 0 && cx[0].SpendNanos != 900_000_000 {
		t.Fatalf("SpendNanos=%d, want 900M (post-roll, not the stale 1B)", cx[0].SpendNanos)
	}

	// The reset worker then runs: it must be a no-op for this scope (the row
	// is already fresh), and the gap spend must survive it.
	if n, err := f.s.ResetBudgets(ctx); err != nil || n != 0 {
		t.Fatalf("reset after inline roll: %d %v, want 0 rows (no-op)", n, err)
	}
	k, _ = f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 900_000_000 {
		t.Fatalf("after reset: spend_nanos=%d, want 900M (not erased)", k.SpendNanos)
	}
	// More spend in the same fresh window accumulates normally.
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(250_000_000, now)}); err != nil {
		t.Fatal(err)
	}
	k, _ = f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 1_150_000_000 {
		t.Fatalf("spend_nanos=%d, want 1.15B (accumulation in the rolled window)", k.SpendNanos)
	}
}

// TestRecordSpend_TwoWindowsWithoutReset guards the reviewer's worst case:
// the reset worker never runs. Two consecutive windows must each carry
// exactly their own spend, with correct crossings in each, purely from the
// inline roll.
func TestRecordSpend_TwoWindowsWithoutReset(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()

	// Window A: fill to the cap; both thresholds fire once.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) || !have(cx, "key", 100) {
		t.Fatalf("window A: got %+v, want 80 and 100", cx)
	}
	// Let window A expire (worker NEVER runs) and write into the fresh
	// window B: it rolls from zero and fires both thresholds again.
	forceDue(t, f)
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) || !have(cx, "key", 100) {
		t.Fatalf("window B: got %+v, want 80 and 100 (no reset worker ran)", cx)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 1_050_000_000 {
		t.Fatalf("window B: spend_nanos=%d, want 1.05B (A's 1.05B discarded by the roll)", k.SpendNanos)
	}
	// Window B again: the marker holds at pct 100, so nothing re-fires even
	// though spend keeps growing.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("window B overage: got %+v, want zero crossings (one-shot per window)", cx)
	}
	k, _ = f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 2_100_000_000 {
		t.Fatalf("window B: spend_nanos=%d, want 2.1B", k.SpendNanos)
	}
}

// TestRecordSpend_WindowChargeInServerTimezone is the A2 end-to-end half:
// RecordSpend must derive the in-window charging boundary in the SERVER's
// timezone (loaded from settings), not UTC. A monthly window in Asia/Taipei
// ending at the 1st-of-month local midnight has its start one month back in
// WALL-CLOCK Taipei. Go's AddDate normalizes day overflow (it does not clamp),
// so subtracting a month in the UTC frame drifts several days — that is the
// A2 bug. The reset is a fixed FUTURE monthly reset (2027-04-01 00:00 Taipei
// = 2027-03-31 16:00 UTC) so the window is active for the duration of the test
// and the math is independent of the host's TZ.
//
//	Taipei window:  [2027-02-28 16:00 UTC, 2027-03-31 16:00 UTC)
//	UTC-math window: [2027-03-03 16:00 UTC, 2027-03-31 16:00 UTC)  (bug)
//
// A record at 2027-03-01 00:00 UTC is inside the Taipei window but outside the
// UTC-math window, so it is charged only by the correct (server-tz) logic.
func TestRecordSpend_WindowChargeInServerTimezone(t *testing.T) {
	f := newFixture(t, "monthly")
	ctx := context.Background()
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei"
	if _, err := f.s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	reset := time.Date(2027, 3, 31, 16, 0, 0, 0, time.UTC) // 2027-04-01 00:00 Taipei, a monthly reset
	forceWindowAt(t, f, reset)
	inWindow := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)   // inside the Taipei window, outside UTC-math
	outWindow := time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC) // before the Taipei window start
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, outWindow), f.rec(250, inWindow)}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 250 {
		t.Fatalf("spend_nanos=%d, want 250 (only the in-window record, per the server timezone)", k.SpendNanos)
	}
	// Both records are still logged.
	rows, err := f.s.QuerySpend(ctx, repo.SpendFilter{From: outWindow.Add(-time.Hour), To: reset.Add(time.Hour), GroupBy: "model"})
	if err != nil || len(rows) != 1 || rows[0].Requests != 2 {
		t.Fatalf("rows=%+v err=%v, want 2 logged records", rows, err)
	}
}

// TestRecordSpend_MarkerWindowIdentityInServerTimezone verifies the one-shot
// notify marker stores the window start derived in the SERVER's timezone (A2
// on the marker path). Same fixed future monthly reset as above: the marker
// must hold 2027-02-28 16:00 UTC (Taipei wall-clock month start), not the
// UTC-math drift 2027-03-03 16:00 UTC.
func TestRecordSpend_MarkerWindowIdentityInServerTimezone(t *testing.T) {
	f := newFixture(t, "monthly")
	ctx := context.Background()
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei"
	if _, err := f.s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	reset := time.Date(2027, 3, 31, 16, 0, 0, 0, time.UTC)
	forceWindowAt(t, f, reset)
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) {
		t.Fatalf("got %+v, want the 80 key crossing", cx)
	}
	got, err := f.s.QueryForTest(ctx, `SELECT budget_notified_window FROM api_keys WHERE id=$1`, f.k.ID)
	if err != nil {
		t.Fatal(err)
	}
	marker, ok := got.(time.Time)
	if !ok {
		t.Fatalf("budget_notified_window type = %T, want time.Time", got)
	}
	want := time.Date(2027, 2, 28, 16, 0, 0, 0, time.UTC) // Taipei wall-clock month start
	if !marker.Equal(want) {
		t.Fatalf("budget_notified_window=%v, want %v (server-timezone window start)", marker, want)
	}
}

// TestUpdateKey_ReraimsNotifyMarker is A11: editing a budget mid-window
// (raising the cap after the key already hit 100%) must re-arm the one-shot
// marker so the NEW thresholds fire again.
func TestUpdateKey_ReraimsNotifyMarker(t *testing.T) {
	f := newFixture(t, "daily") // budget_nanos = 1e9
	ctx := context.Background()
	now := time.Now()
	// Hit 100%: marker now holds the window start at pct 100.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 100) {
		t.Fatalf("setup: got %+v, want the 100 crossing", cx)
	}
	// Admin raises the cap to 2e9. The old pct-100 marker would suppress the
	// new 80% (1.6e9) and 100% (2e9) crossings without the re-arm.
	kin, err := f.s.GetKey(ctx, f.k.ID)
	if err != nil {
		t.Fatal(err)
	}
	kin.BudgetNanos = 2_000_000_000
	if _, err := f.s.UpdateKey(ctx, f.k.ID, repo.KeyInput{Alias: kin.Alias, GroupID: kin.GroupID, Models: kin.Models,
		BudgetType: kin.BudgetType, BudgetNanos: kin.BudgetNanos, RPMLimit: kin.RPMLimit, TPMLimit: kin.TPMLimit, ExpiresAt: kin.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	// 1.05B -> 2.1B crosses the new 80% and 100%: both must fire again.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "key", 80) {
		t.Fatalf("after cap raise: got %+v, want the new 80 crossing (1.6e9)", cx)
	}
	if !have(cx, "key", 100) {
		t.Fatalf("after cap raise: got %+v, want the new 100 crossing (2e9)", cx)
	}
	// A no-op edit (same cap and type) must NOT clear a fresh marker: the
	// key stays one-shot within its window.
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(10_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	kin, _ = f.s.GetKey(ctx, f.k.ID)
	if _, err := f.s.UpdateKey(ctx, f.k.ID, repo.KeyInput{Alias: kin.Alias, GroupID: kin.GroupID, Models: kin.Models,
		BudgetType: kin.BudgetType, BudgetNanos: kin.BudgetNanos, RPMLimit: kin.RPMLimit, TPMLimit: kin.TPMLimit, ExpiresAt: kin.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(10_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("no-op edit: got %+v, want zero crossings (marker must not be cleared)", cx)
	}
}

// TestUpdateGroup_ReraimsNotifyMarker is A11 on the group scope: raising a
// group's cap mid-window after it hit 100% re-arms its marker.
func TestUpdateGroup_ReraimsNotifyMarker(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	now := time.Now()
	// Give the fixture group a daily 1e9 budget.
	gin, err := f.s.GetGroup(ctx, f.g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateGroup(ctx, f.g.ID, repo.GroupInput{Name: gin.Name, BudgetType: "daily", BudgetNanos: 1_000_000_000,
		RPMLimit: gin.RPMLimit, TPMLimit: gin.TPMLimit, Models: &gin.Models}); err != nil {
		t.Fatal(err)
	}
	// Fill the group window to 100%: the group marker holds pct 100.
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "group", 100) {
		t.Fatalf("setup: got %+v, want the 100 group crossing", cx)
	}
	// Raise the cap to 2e9 — the re-arm must let the new thresholds fire.
	if _, err := f.s.UpdateGroup(ctx, f.g.ID, repo.GroupInput{Name: gin.Name, BudgetType: "daily", BudgetNanos: 2_000_000_000,
		RPMLimit: gin.RPMLimit, TPMLimit: gin.TPMLimit, Models: &gin.Models}); err != nil {
		t.Fatal(err)
	}
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(1_050_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !have(cx, "group", 80) || !have(cx, "group", 100) {
		t.Fatalf("after cap raise: got %+v, want the group's new 80 and 100 crossings", cx)
	}
}

// TestRecordSpend_TotalBudgetUnaffectedByRoll is A3's total-budget guard:
// the inline roll must never touch a total budget — no window, no expiry,
// plain accumulation forever.
func TestRecordSpend_TotalBudgetUnaffectedByRoll(t *testing.T) {
	f := newFixture(t, "total")
	ctx := context.Background()
	now := time.Now()
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(500, now)}); err != nil {
		t.Fatal(err)
	}
	// total never rolls, so nothing can force it "due" — the row's
	// budget_reset_at stays NULL. Verify accumulation over a stale-looking
	// old record plus a fresh one.
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(300, now.AddDate(0, -1, 0))}); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 800 {
		t.Fatalf("spend_nanos=%d, want 800 (total: everything counts, no roll)", k.SpendNanos)
	}
	if k.BudgetResetAt != nil {
		t.Fatalf("budget_reset_at=%v, want NULL for total", k.BudgetResetAt)
	}
	// The reset worker also leaves total budgets alone.
	if n, _ := f.s.ResetBudgets(ctx); n != 0 {
		t.Fatalf("reset %d rows; total budgets must never reset", n)
	}
	k, _ = f.s.GetKey(ctx, f.k.ID)
	if k.SpendNanos != 800 {
		t.Fatalf("after reset: spend_nanos=%d, want 800", k.SpendNanos)
	}
}
