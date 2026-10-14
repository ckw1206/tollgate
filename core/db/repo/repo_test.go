package repo

import (
	"testing"
	"time"
)

// Fixed, deterministic instants so tests never depend on the wall clock.
var (
	// now is mid-day, inside every "active" window and after every "expired" one.
	now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	// Active daily: window resets tomorrow (in the future).
	activeDailyReset = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	// Expired daily: window already reset this morning (in the past).
	expiredDailyReset = time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	// Active monthly: window resets a month out (in the future).
	activeMonthlyReset = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	// Active monthly window start = reset - 1 month.
	activeMonthlyStart = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

	utc = time.UTC
)

// ptr returns a pointer to a copy of v so each test case owns distinct storage.
func ptr(v time.Time) *time.Time { c := v; return &c }

func TestOverBudget(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		want   bool
	}{
		{
			name:   "active daily at cap",
			limits: Limits{BudgetType: "daily", BudgetNanos: 100, SpendNanos: 100, BudgetResetAt: ptr(activeDailyReset)},
			want:   true,
		},
		{
			name:   "active daily under cap",
			limits: Limits{BudgetType: "daily", BudgetNanos: 100, SpendNanos: 50, BudgetResetAt: ptr(activeDailyReset)},
			want:   false,
		},
		{
			name:   "expired daily at cap (reset_at in the past)",
			limits: Limits{BudgetType: "daily", BudgetNanos: 100, SpendNanos: 100, BudgetResetAt: ptr(expiredDailyReset)},
			want:   false,
		},
		{
			name:   "total at cap",
			limits: Limits{BudgetType: "total", BudgetNanos: 100, SpendNanos: 100},
			want:   true,
		},
		{
			name:   "unlimited at cap",
			limits: Limits{BudgetType: "unlimited", BudgetNanos: 100, SpendNanos: 100},
			want:   false,
		},
		{
			name:   "monthly under cap",
			limits: Limits{BudgetType: "monthly", BudgetNanos: 100, SpendNanos: 50, BudgetResetAt: ptr(activeMonthlyReset)},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.limits.OverBudget(now); got != tc.want {
				t.Errorf("OverBudget(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestEffectiveSpend(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		want   int64
	}{
		{
			name:   "expired daily",
			limits: Limits{BudgetType: "daily", SpendNanos: 100, BudgetResetAt: ptr(expiredDailyReset)},
			want:   0,
		},
		{
			name:   "active daily",
			limits: Limits{BudgetType: "daily", SpendNanos: 75, BudgetResetAt: ptr(activeDailyReset)},
			want:   75,
		},
		{
			name:   "total with a past reset is still charged",
			limits: Limits{BudgetType: "total", SpendNanos: 99, BudgetResetAt: ptr(expiredDailyReset)},
			want:   99,
		},
		{
			name:   "total without reset",
			limits: Limits{BudgetType: "total", SpendNanos: 42},
			want:   42,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.limits.EffectiveSpend(now); got != tc.want {
				t.Errorf("EffectiveSpend(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestWindowStart(t *testing.T) {
	cases := []struct {
		name string
		lims Limits
		loc  *time.Location
		want *time.Time
	}{
		{
			name: "active daily",
			lims: Limits{BudgetType: "daily", BudgetResetAt: ptr(activeDailyReset)},
			loc:  utc,
			want: ptr(expiredDailyReset), // 2026-10-14T00:00:00Z = reset - 1d
		},
		{
			name: "expired daily",
			lims: Limits{BudgetType: "daily", BudgetResetAt: ptr(expiredDailyReset)},
			loc:  utc,
			want: ptr(expiredDailyReset), // expired window starts at the reset instant
		},
		{
			name: "active monthly",
			lims: Limits{BudgetType: "monthly", BudgetResetAt: ptr(activeMonthlyReset)},
			loc:  utc,
			want: ptr(activeMonthlyStart), // reset - 1 month
		},
		{
			name: "total",
			lims: Limits{BudgetType: "total", BudgetResetAt: ptr(activeDailyReset)},
			loc:  utc,
			want: ptr(windowSentinelStart), // fixed epoch identity, never expires
		},
		{
			name: "total without reset",
			lims: Limits{BudgetType: "total"},
			loc:  utc,
			want: ptr(windowSentinelStart),
		},
		{
			name: "unlimited",
			lims: Limits{BudgetType: "unlimited"},
			loc:  utc,
			want: ptr(windowSentinelStart),
		},
		{
			name: "nil BudgetResetAt daily",
			lims: Limits{BudgetType: "daily"},
			loc:  utc,
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.lims.WindowStart(now, tc.loc)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("WindowStart(%s) = %v (nil=%v), want %v (nil=%v)",
					tc.name, got, got == nil, tc.want, tc.want == nil)
			}
			if got != nil && !got.Equal(*tc.want) {
				t.Errorf("WindowStart(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestWindowStart_ServerTimezone is machine-TZ-independent: the instants and
// 'now' are all fixed, and the window is derived in the SERVER's timezone, not
// the host's time.Local (A2). Before the fix, AddDate ran in the scanned
// time's Location (UTC), so monthly starts drifted 1-3 days for UTC+
// timezones.
func TestWindowStart_ServerTimezone(t *testing.T) {
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("no tzdata on this host: %v", err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("no tzdata on this host: %v", err)
	}
	// Lord Howe Island: UTC+10:30, DST (UTC+11) from the first Sunday of
	// October — a daily window reset at local midnight straddles the
	// transition.
	lordHowe, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Fatalf("no tzdata on this host: %v", err)
	}

	// 'now' fixed, before every reset instant below, so every window is
	// active. Independent of the machine's TZ by construction.
	fixedNow := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		lims Limits
		loc  *time.Location
		want time.Time
	}{
		{
			// Reviewer repro: server timezone Asia/Taipei, monthly window
			// ending 2026-03-31 16:00Z. UTC math says 2026-03-03; the true
			// calendar start is 2026-02-28 (Taipei: March 31 minus one month).
			name: "monthly Asia/Taipei 2026-03-31 16:00Z",
			lims: Limits{BudgetType: "monthly", BudgetResetAt: ptr(time.Date(2026, 3, 31, 16, 0, 0, 0, time.UTC))},
			loc:  taipei,
			want: time.Date(2026, 2, 28, 16, 0, 0, 0, time.UTC),
		},
		{
			// Reviewer repro: same timezone, October monthly window. UTC math
			// says 2026-10-01; the true start is 2026-09-30 (October 31 minus
			// one month, clamped to the end of September).
			name: "monthly Asia/Taipei 2026-10-31 16:00Z",
			lims: Limits{BudgetType: "monthly", BudgetResetAt: ptr(time.Date(2026, 10, 31, 16, 0, 0, 0, time.UTC))},
			loc:  taipei,
			want: time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC),
		},
		{
			// Daily window across a DST boundary: America/New_York ends DST at
			// 2026-11-01 06:00Z. Local-midnight reset 2026-11-02 05:00Z
			// (EST) means the previous local midnight is 2026-11-01 04:00Z
			// (EDT) — a 23h window. UTC math would give 24h (2026-11-01
			// 05:00Z), charging an hour of early-window spend to nothing.
			name: "daily America/New_York across DST end",
			lims: Limits{BudgetType: "daily", BudgetResetAt: ptr(time.Date(2026, 11, 2, 5, 0, 0, 0, time.UTC))},
			loc:  newYork,
			want: time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC),
		},
		{
			// Lord Howe Island: UTC+10:30 -> UTC+11 at 2026-10-04 15:30Z.
			// Local-midnight reset 2026-10-04 13:00Z (LEST) means the
			// previous local midnight is 2026-10-03 13:30Z (LHST) — a 23.5h
			// window. UTC math would give 24h (2026-10-03 13:00Z).
			name: "daily Australia/Lord_Howe across DST start",
			lims: Limits{BudgetType: "daily", BudgetResetAt: ptr(time.Date(2026, 10, 4, 13, 0, 0, 0, time.UTC))},
			loc:  lordHowe,
			want: time.Date(2026, 10, 3, 13, 30, 0, 0, time.UTC),
		},
		{
			// Same reset instant as the Taipei case above, but with the UTC
			// frame: the window start is 8 days later. This is the A2 bug in
			// a single case — the frame changes the answer, so it must be the
			// SERVER's timezone.
			name: "same instant, UTC frame",
			lims: Limits{BudgetType: "monthly", BudgetResetAt: ptr(time.Date(2026, 3, 31, 16, 0, 0, 0, time.UTC))},
			loc:  utc,
			want: time.Date(2026, 3, 3, 16, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.lims.WindowStart(fixedNow, tc.loc)
			if got == nil {
				t.Fatal("WindowStart = nil, want a window")
			}
			if !got.Equal(tc.want) {
				t.Fatalf("WindowStart = %v, want %v", got, tc.want)
			}
			// The result must be a plain absolute instant (same instant in
			// UTC), so it compares exactly against stored TIMESTAMPTZ values.
			if got.UTC() != tc.want.UTC() {
				t.Fatalf("WindowStart = %v, want instant %v", got, tc.want)
			}
		})
	}
}

// TestInWindowCosts_ServerTimezone pins the A2 charging boundary at the unit
// level with fixed instants: a record counts iff its CreatedAt is at/after
// the window start derived in the SERVER's timezone. The same record is
// charged under the Taipei frame but not under the UTC frame — the frames
// must not be interchangeable.
func TestInWindowCosts_ServerTimezone(t *testing.T) {
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("no tzdata on this host: %v", err)
	}
	// 'now' inside the window, well before every reset below.
	mid := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	// Monthly window ending 2026-03-31 16:00Z.
	lims := map[string]Limits{
		"k1": {BudgetType: "monthly", BudgetResetAt: ptr(time.Date(2026, 3, 31, 16, 0, 0, 0, time.UTC))},
	}
	recs := []SpendRecord{
		{KeyID: "k1", CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), CostNanos: 100}, // inside Taipei only
		{KeyID: "k1", CreatedAt: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), CostNanos: 50},  // inside Taipei only
		{KeyID: "k1", CreatedAt: time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC), CostNanos: 25}, // outside both frames
	}
	gotTpei := inWindowCosts(recs, lims, func(r SpendRecord) string { return r.KeyID }, mid, taipei)
	if gotTpei["k1"] != 150 {
		t.Fatalf("Taipei frame: charged %d, want 150 (window starts 2026-02-28 16:00Z)", gotTpei["k1"])
	}
	gotUTC := inWindowCosts(recs, lims, func(r SpendRecord) string { return r.KeyID }, mid, utc)
	if gotUTC["k1"] != 0 {
		t.Fatalf("UTC frame: charged %d, want 0 (Go's AddDate normalizes Feb 31 -> Mar 3, so the UTC window starts 2026-03-03 16:00Z)", gotUTC["k1"])
	}
	// total: the epoch sentinel admits every record regardless of frame.
	limsTotal := map[string]Limits{"k1": {BudgetType: "total"}}
	gotTotal := inWindowCosts(recs, limsTotal, func(r SpendRecord) string { return r.KeyID }, mid, utc)
	if gotTotal["k1"] != 175 {
		t.Fatalf("total: charged %d, want 175 (all records)", gotTotal["k1"])
	}
	// unlimited: same.
	limsUnl := map[string]Limits{"k1": {BudgetType: "unlimited"}}
	gotUnl := inWindowCosts(recs, limsUnl, func(r SpendRecord) string { return r.KeyID }, mid, utc)
	if gotUnl["k1"] != 175 {
		t.Fatalf("unlimited: charged %d, want 175", gotUnl["k1"])
	}
}

// TestServerTimezone_Fallback guards the defensive fallback: an unknown or
// missing timezone name must degrade to UTC, never panic (the spend path
// runs on every flush and the setting is admin-editable).
func TestServerTimezone_Fallback(t *testing.T) {
	for _, name := range []string{"", "Mars/Olympus", "Not/AZone"} {
		if loc := serverTimezone(name); loc != time.UTC {
			t.Errorf("serverTimezone(%q) = %v, want UTC", name, loc)
		}
	}
	if loc := serverTimezone("Asia/Taipei"); loc == nil {
		t.Error("serverTimezone(Asia/Taipei) = nil, want a location")
	}
}
