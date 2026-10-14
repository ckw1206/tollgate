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
		name   string
		limits Limits
		want   *time.Time
	}{
		{
			name:   "active daily",
			limits: Limits{BudgetType: "daily", BudgetResetAt: ptr(activeDailyReset)},
			want:   ptr(expiredDailyReset), // 2026-10-14T00:00:00Z = reset - 1d
		},
		{
			name:   "expired daily",
			limits: Limits{BudgetType: "daily", BudgetResetAt: ptr(expiredDailyReset)},
			want:   ptr(expiredDailyReset), // expired window starts at the reset instant
		},
		{
			name:   "active monthly",
			limits: Limits{BudgetType: "monthly", BudgetResetAt: ptr(activeMonthlyReset)},
			want:   ptr(activeMonthlyStart), // reset - 1 month
		},
		{
			name:   "total",
			limits: Limits{BudgetType: "total", BudgetResetAt: ptr(activeDailyReset)},
			want:   nil,
		},
		{
			name:   "nil BudgetResetAt daily",
			limits: Limits{BudgetType: "daily"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.limits.WindowStart(now)
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
