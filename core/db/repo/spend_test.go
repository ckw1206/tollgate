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
	if _, err := f.s.QuerySpend(ctx, repo.SpendFilter{GroupBy: "week"}); err != repo.ErrGroupBy {
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

	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil || len(cx) != 1 || cx[0].Threshold != 80 {
		t.Fatalf("setup: cx=%+v err=%v, want one 80 crossing", cx, err)
	}
	// Force the window due and run the reset: zeroes spend, schedules next
	// window, and clears the notify marker.
	forceDue(t, f)
	if _, err := f.s.ResetBudgets(ctx); err != nil {
		t.Fatal(err)
	}
	// A fresh batch crossing 80% again must fire once more (marker reset).
	cx, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(900_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 1 || cx[0].Threshold != 80 || cx[0].SpendNanos != 900_000_000 {
		t.Fatalf("after reset: got %+v, want one fresh 80 crossing at 900M", cx)
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

func TestRecordSpend_TotalBudgetNeverCrosses(t *testing.T) {
	f := newFixture(t, "total")
	ctx := context.Background()
	now := time.Now()
	cx, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(5_000_000_000, now)})
	if err != nil {
		t.Fatal(err)
	}
	if len(cx) != 0 {
		t.Fatalf("total budget: got %+v, want zero crossings", cx)
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
