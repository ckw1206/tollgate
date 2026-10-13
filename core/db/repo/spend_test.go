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
	if err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, now), f.rec(250, now)}); err != nil {
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
