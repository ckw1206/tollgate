package repo

import (
	"context"
	"errors"
)

// GlobalID is the global scope's id: the single server_settings row. It is
// passed where key and group scopes pass a UUID; pgx encodes the string into
// the integer id column.
const GlobalID = "1"

// ErrGlobalBudgetType: the global cap is unlimited, daily or monthly. A total
// cap would never reset and stop the gateway for good once reached.
var ErrGlobalBudgetType = errors.New("budget_type must be unlimited, daily or monthly")

// ErrGlobalBudgetAmount: a daily or monthly global cap must be above zero.
var ErrGlobalBudgetAmount = errors.New("budget_nanos must be > 0 for a daily or monthly cap")

const globalCols = `budget_type, budget_nanos, spend_nanos, budget_reset_at`

// GlobalBudget returns the server-wide cap (#31). Its RPM/TPM are always nil.
func (s *Store) GlobalBudget(ctx context.Context) (Limits, error) {
	var l Limits
	err := s.pool.QueryRow(ctx, `SELECT `+globalCols+` FROM server_settings WHERE id=1`).
		Scan(&l.BudgetType, &l.BudgetNanos, &l.SpendNanos, &l.BudgetResetAt)
	return l, err
}

// SetGlobalBudget sets the server-wide cap. Changing the type starts a window
// in the server timezone and counts the spend already logged in it: every
// request is global spend, so unlike a key or group (whose counter only
// accrues while it has a budget) the logs are the exact current-window
// figure, and a cap switched on mid-day sees the day's spend so far. With
// SPEND_RETENTION_DAYS shorter than a month, a monthly count covers only the
// retained logs. The same type with a new amount keeps the counter. Any
// change re-arms the 80%/100% notify marker, as UpdateKey does (A11).
//
// The settings row is locked first, like UpdateSettings, so this serializes
// with spend batches (RecordSpend locks the row FOR NO KEY UPDATE). Returns
// the cap before and after the change, both read under that lock.
func (s *Store) SetGlobalBudget(ctx context.Context, budgetType string, nanos int64) (before, after Limits, err error) {
	if budgetType != "unlimited" && budgetType != "daily" && budgetType != "monthly" {
		return before, after, ErrGlobalBudgetType
	}
	if budgetType == "unlimited" {
		nanos = 0
	} else if nanos <= 0 {
		// A $0 cap would refuse every paid request on the whole gateway, and
		// detectCrossings never alerts on a zero cap. Keys and groups allow it;
		// the gateway-wide breaker doesn't.
		return before, after, ErrGlobalBudgetAmount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback(ctx)
	if err := tx.QueryRow(ctx, `SELECT `+globalCols+` FROM server_settings WHERE id=1 FOR UPDATE`).
		Scan(&before.BudgetType, &before.BudgetNanos, &before.SpendNanos, &before.BudgetResetAt); err != nil {
		return before, after, err
	}
	var l Limits
	err = tx.QueryRow(ctx, `
		UPDATE server_settings s SET
			spend_nanos = CASE
				WHEN s.budget_type = $1::budget_type THEN s.spend_nanos
				WHEN $1::budget_type = 'unlimited' THEN 0
				ELSE COALESCE((SELECT sum(l.cost_nanos) FROM spend_logs l WHERE l.created_at >=
					date_trunc(CASE $1::budget_type WHEN 'daily' THEN 'day' ELSE 'month' END, now() AT TIME ZONE s.timezone)
						AT TIME ZONE s.timezone), 0)::bigint
			END,
			budget_reset_at = CASE WHEN s.budget_type = $1::budget_type THEN s.budget_reset_at ELSE next_budget_reset($1) END,
			budget_notified_window = CASE WHEN s.budget_type <> $1::budget_type OR s.budget_nanos <> $2 THEN NULL ELSE s.budget_notified_window END,
			budget_notified_pct    = CASE WHEN s.budget_type <> $1::budget_type OR s.budget_nanos <> $2 THEN 0 ELSE s.budget_notified_pct END,
			budget_type = $1, budget_nanos = $2
		WHERE s.id = 1 RETURNING `+globalCols, budgetType, nanos).
		Scan(&l.BudgetType, &l.BudgetNanos, &l.SpendNanos, &l.BudgetResetAt)
	if err != nil {
		return before, after, err
	}
	return before, l, tx.Commit(ctx)
}
