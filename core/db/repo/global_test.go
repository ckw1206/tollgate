package repo_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// #31: the server-wide cap. Unlimited by default; while unlimited, spend
// batches don't touch its counter.
func TestGlobalBudget_DefaultUnlimited(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(500, time.Now())}); err != nil {
		t.Fatal(err)
	}
	g, err := f.s.GlobalBudget(ctx)
	if err != nil || g.BudgetType != "unlimited" || g.SpendNanos != 0 || g.BudgetResetAt != nil {
		t.Fatalf("default global: %+v %v", g, err)
	}
	if _, _, err := f.s.SetGlobalBudget(ctx, "total", 1); !errors.Is(err, repo.ErrGlobalBudgetType) {
		t.Fatalf("total global cap: err = %v, want ErrGlobalBudgetType", err)
	}
	if _, _, err := f.s.SetGlobalBudget(ctx, "daily", 0); !errors.Is(err, repo.ErrGlobalBudgetAmount) {
		t.Fatalf("$0 daily global cap: err = %v, want ErrGlobalBudgetAmount", err)
	}
}

// Switching the cap on counts the window's spend already logged; afterwards
// every record accrues, a crossing fires with scope "global", and the auth
// lookup carries the cap.
func TestGlobalBudget_CountsAccruesAndCrosses(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	now := time.Now()
	// Today's spend (before the cap exists) and yesterday's (outside the window).
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(300e6, now.Add(-time.Millisecond)), f.rec(999e6, now.Add(-48*time.Hour))}); err != nil {
		t.Fatal(err)
	}
	_, g, err := f.s.SetGlobalBudget(ctx, "daily", 1e9)
	if err != nil {
		t.Fatal(err)
	}
	if g.SpendNanos != 300e6 || g.BudgetResetAt == nil || !g.BudgetResetAt.After(now) {
		t.Fatalf("switched on: %+v, want today's 300e6 and a future reset", g)
	}
	cs, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(550e6, time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Scope != "global" || cs[0].Threshold != 80 || cs[0].Label != "global" || cs[0].SpendNanos != 850e6 {
		t.Fatalf("crossings %+v, want global 80 at 850e6", cs)
	}
	if g, _ := f.s.GlobalBudget(ctx); g.SpendNanos != 850e6 {
		t.Fatalf("global spend %d, want 850e6", g.SpendNanos)
	}
	// Same type, new amount: counter kept, marker re-armed.
	if _, g, _ := f.s.SetGlobalBudget(ctx, "daily", 2e9); g.SpendNanos != 850e6 {
		t.Fatalf("amount change reset spend: %+v", g)
	}
	// The auth lookup carries the cap for admit().
	raw, _, err := f.s.CreateKey(ctx, f.u.ID, repo.KeyInput{Alias: "k3", GroupID: f.g.ID, BudgetType: "unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.s.AuthByKeyHash(ctx, repo.HashToken(raw))
	if err != nil || info.Global.BudgetType != "daily" || info.Global.BudgetNanos != 2e9 || info.Global.SpendNanos != 850e6 {
		t.Fatalf("auth global: %+v %v", info.Global, err)
	}
	// Back to unlimited clears it.
	if _, g, _ := f.s.SetGlobalBudget(ctx, "unlimited", 5); g.SpendNanos != 0 || g.BudgetNanos != 0 || g.BudgetResetAt != nil {
		t.Fatalf("unlimited: %+v", g)
	}
}

// The reset worker zeroes a due global window, and a timezone change
// reschedules and recounts it like any key or group (#27).
func TestGlobalBudget_ResetAndTimezone(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	if _, _, err := f.s.SetGlobalBudget(ctx, "daily", 1e9); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(100, time.Now())}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ExecForTest(ctx, `UPDATE server_settings SET budget_reset_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := f.s.ResetBudgets(ctx); err != nil || n != 1 {
		t.Fatalf("reset rows %d %v, want the global row", n, err)
	}
	g, _ := f.s.GlobalBudget(ctx)
	if g.SpendNanos != 0 || !g.BudgetResetAt.After(time.Now()) {
		t.Fatalf("after reset: %+v", g)
	}
	// Zone change: recounted from the logs since local midnight in Taipei, and rescheduled.
	st, _ := f.s.GetSettings(ctx)
	st.Timezone = "Asia/Taipei"
	before := time.Now()
	if _, err := f.s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	tpe, _ := time.LoadLocation("Asia/Taipei")
	g, _ = f.s.GlobalBudget(ctx)
	if !g.BudgetResetAt.Equal(nextBoundary(before, tpe, false)) && !g.BudgetResetAt.Equal(nextBoundary(time.Now(), tpe, false)) {
		t.Fatalf("global reset %v, want next Taipei midnight", g.BudgetResetAt)
	}
	if g.SpendNanos != 100 {
		t.Fatalf("global spend %d after zone change, want the 100 logged just now", g.SpendNanos)
	}
}

// Concurrent spend batches with a cap set: each locks the settings row and
// then updates it, which must serialize rather than deadlock, and every cost
// must land exactly once. (FOR SHARE here deadlocked nearly every batch.)
func TestGlobalBudget_ConcurrentBatchesDontDeadlock(t *testing.T) {
	f := newFixture(t, "unlimited")
	ctx := context.Background()
	if _, _, err := f.s.SetGlobalBudget(ctx, "daily", 1e15); err != nil {
		t.Fatal(err)
	}
	const workers, batches = 4, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers*batches)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < batches; i++ {
				if _, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(7, time.Now())}); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent RecordSpend: %v", err)
	}
	if g, _ := f.s.GlobalBudget(ctx); g.SpendNanos != 7*workers*batches {
		t.Fatalf("global spend %d, want %d", g.SpendNanos, 7*workers*batches)
	}
}
