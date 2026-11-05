package repo_test

import (
	"context"
	"errors"
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
	if _, err := s.UpdateSettings(ctx, *cur); !errors.Is(err, repo.ErrInvalidTimezone) {
		t.Fatalf("invalid timezone: err = %v, want ErrInvalidTimezone", err)
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

// nextBoundary is next_budget_reset computed in Go: the next local midnight
// (daily) or first of the month (monthly) in loc. windowStart is one period
// earlier: today's midnight or this month's first, in loc.
func nextBoundary(now time.Time, loc *time.Location, monthly bool) time.Time {
	l := now.In(loc)
	if monthly {
		return time.Date(l.Year(), l.Month()+1, 1, 0, 0, 0, 0, loc)
	}
	return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
}

func windowStart(now time.Time, loc *time.Location, monthly bool) time.Time {
	l := now.In(loc)
	if monthly {
		return time.Date(l.Year(), l.Month(), 1, 0, 0, 0, 0, loc)
	}
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

type spent struct {
	at   time.Time
	cost int64
}

// sumIn sums the costs in [from, to).
func sumIn(recs []spent, from, to time.Time) int64 {
	var n int64
	for _, r := range recs {
		if !r.at.Before(from) && r.at.Before(to) {
			n += r.cost
		}
	}
	return n
}

// recounted models UpdateSettings' rule: after a zone change, the counter is
// the spend logged since the current window start in the new zone.
func recounted(recs []spent, now time.Time, to *time.Location, monthly bool) int64 {
	return sumIn(recs, windowStart(now, to, monthly), now.Add(time.Hour))
}

// setZone saves tz and returns the save time. A run that straddles a window
// boundary in either zone is skipped rather than asserted against a guess.
func setZone(t *testing.T, s *repo.Store, from *time.Location, tz string, monthly bool) (*time.Location, time.Time) {
	t.Helper()
	ctx := context.Background()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.Timezone = tz
	before := time.Now()
	if _, err := s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	for _, l := range []*time.Location{from, loc} {
		if !windowStart(before, l, monthly).Equal(windowStart(after, l, monthly)) {
			t.Skipf("save straddled a window boundary in %s", l)
		}
	}
	return loc, after
}

// #27: rows are created BEFORE the zone changes (the existing timezone tests
// all set the zone first, which is why this survived). Spend is spread over
// the last two days and the zone hops east and west (including UTC+14), so at
// any time of day some hops move the window start later and some earlier.
func TestUpdateSettings_TimezoneChangeReschedulesDailyWindows(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	now := time.Now()
	var recs []spent
	var batch []repo.SpendRecord
	for i, ago := range []time.Duration{47 * time.Hour, 30 * time.Hour, 20 * time.Hour, 13 * time.Hour, 9 * time.Hour, 5 * time.Hour, 2 * time.Hour, time.Minute} {
		r := spent{now.Add(-ago), int64(1e6) << i} // distinct powers of two: any wrong subset sums differently
		recs = append(recs, r)
		batch = append(batch, f.rec(r.cost, r.at))
	}
	if _, err := f.s.RecordSpend(ctx, batch); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	counter := k.SpendNanos
	if want := sumIn(recs, windowStart(now, time.UTC, false), now.Add(time.Hour)); counter != want {
		t.Fatalf("setup: counter %d, want %d", counter, want)
	}
	// A daily key whose window expired but the reset worker hasn't run yet:
	// its stale counter must not survive the reschedule.
	_, due, err := f.s.CreateKey(ctx, f.u.ID, repo.KeyInput{Alias: "due", GroupID: f.g.ID, BudgetType: "daily", BudgetNanos: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ExecForTest(ctx, `UPDATE api_keys SET spend_nanos=900, budget_reset_at=now()-interval '1 second' WHERE id=$1`, due.ID); err != nil {
		t.Fatal(err)
	}
	// A total key: no window, must be left alone.
	_, tot, err := f.s.CreateKey(ctx, f.u.ID, repo.KeyInput{Alias: "tot", GroupID: f.g.ID, BudgetType: "total", BudgetNanos: 1e9})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.ExecForTest(ctx, `UPDATE api_keys SET spend_nanos=42 WHERE id=$1`, tot.ID); err != nil {
		t.Fatal(err)
	}

	from := time.UTC
	// Round trips (UTC → Taipei → UTC, Etc/GMT+4 ↔ Etc/GMT+5): the counter
	// depends only on the zone saved, never on the path.
	for _, tz := range []string{"Asia/Taipei", "UTC", "America/Los_Angeles", "Pacific/Kiritimati", "Etc/GMT+12", "UTC", "Etc/GMT+4", "Etc/GMT+5", "Etc/GMT+4", "Etc/GMT+5"} {
		loc, at := setZone(t, f.s, from, tz, false)
		counter = recounted(recs, at, loc, false)
		k, _ := f.s.GetKey(ctx, f.k.ID)
		if k.BudgetResetAt == nil || !k.BudgetResetAt.Equal(nextBoundary(at, loc, false)) {
			t.Fatalf("%s: reset_at = %v, want the next local midnight", tz, k.BudgetResetAt)
		}
		if k.SpendNanos != counter {
			t.Fatalf("%s: spend = %d, want %d", tz, k.SpendNanos, counter)
		}
		d, _ := f.s.GetKey(ctx, due.ID)
		if d.SpendNanos != 0 || d.BudgetResetAt == nil || !d.BudgetResetAt.After(at) {
			t.Fatalf("%s: expired key: reset_at=%v spend=%d; want a future reset and the stale 900 gone", tz, d.BudgetResetAt, d.SpendNanos)
		}
		from = loc
	}
	tk, _ := f.s.GetKey(ctx, tot.ID)
	if tk.BudgetResetAt != nil || tk.SpendNanos != 42 {
		t.Fatalf("total key touched: reset_at=%v spend=%d", tk.BudgetResetAt, tk.SpendNanos)
	}
}

// A monthly group: its counter (which includes key-less playground rows)
// follows the same rule over month windows.
func TestUpdateSettings_TimezoneChangeReschedulesMonthlyGroup(t *testing.T) {
	f := newFixture(t, "total")
	ctx := context.Background()
	if err := f.s.ExecForTest(ctx, `UPDATE groups SET budget_type='monthly', budget_nanos=1000000000000,
		budget_reset_at=next_budget_reset('monthly') WHERE id=$1`, f.g.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var recs []spent
	var batch []repo.SpendRecord
	for i, ago := range []time.Duration{40 * 24 * time.Hour, 20 * 24 * time.Hour, 10 * 24 * time.Hour, 26 * time.Hour, 14 * time.Hour, time.Minute} {
		r := spent{now.Add(-ago), int64(1e6) << i}
		recs = append(recs, r)
		rec := f.rec(r.cost, r.at)
		if i%2 == 1 {
			rec.KeyID, rec.Source = "", "playground"
		}
		batch = append(batch, rec)
	}
	if _, err := f.s.RecordSpend(ctx, batch); err != nil {
		t.Fatal(err)
	}
	g, _ := f.s.GetGroup(ctx, f.g.ID)
	counter := g.SpendNanos
	from := time.UTC
	for _, tz := range []string{"Pacific/Kiritimati", "Etc/GMT+12", "Asia/Taipei", "UTC"} {
		loc, at := setZone(t, f.s, from, tz, true)
		counter = recounted(recs, at, loc, true)
		g, _ := f.s.GetGroup(ctx, f.g.ID)
		if g.BudgetResetAt == nil || !g.BudgetResetAt.Equal(nextBoundary(at, loc, true)) {
			t.Fatalf("%s: group reset_at = %v, want the next local month start", tz, g.BudgetResetAt)
		}
		if g.SpendNanos != counter {
			t.Fatalf("%s: group spend = %d, want %d", tz, g.SpendNanos, counter)
		}
		from = loc
	}
}

// A threshold already reported in the window does not fire again because the
// zone changed; the next one still does.
func TestUpdateSettings_TimezoneChangeKeepsCrossingsOneShot(t *testing.T) {
	f := newFixture(t, "daily") // budget 1e9
	ctx := context.Background()
	cs, err := f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(850e6, time.Now().Add(-time.Millisecond))})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Scope != "key" || cs[0].Threshold != 80 {
		t.Fatalf("setup: crossings %+v, want key 80", cs)
	}
	setZone(t, f.s, time.UTC, "Asia/Taipei", false)
	cs, err = f.s.RecordSpend(ctx, []repo.SpendRecord{f.rec(200e6, time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Scope != "key" || cs[0].Threshold != 100 {
		t.Fatalf("after zone change: crossings %+v, want only key 100", cs)
	}
}

// Saving settings without changing the timezone leaves every window as-is.
func TestUpdateSettings_SameTimezoneLeavesWindows(t *testing.T) {
	f := newFixture(t, "daily")
	ctx := context.Background()
	pinned := time.Now().Add(36 * time.Hour).UTC().Truncate(time.Second)
	if err := f.s.ExecForTest(ctx, `UPDATE api_keys SET budget_reset_at=$2, spend_nanos=7 WHERE id=$1`, f.k.ID, pinned); err != nil {
		t.Fatal(err)
	}
	st, _ := f.s.GetSettings(ctx)
	st.ServerName = "Renamed"
	if _, err := f.s.UpdateSettings(ctx, *st); err != nil {
		t.Fatal(err)
	}
	k, _ := f.s.GetKey(ctx, f.k.ID)
	if k.BudgetResetAt == nil || !k.BudgetResetAt.Equal(pinned) || k.SpendNanos != 7 {
		t.Fatalf("reset_at=%v spend=%d, want %v and 7 untouched", k.BudgetResetAt, k.SpendNanos, pinned)
	}
}
