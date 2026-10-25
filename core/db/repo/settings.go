package repo

import (
	"context"
	"fmt"
	"time"
)

type Settings struct {
	ServerName       string    `json:"server_name"`
	ServerIcon       string    `json:"server_icon"`
	Timezone         string    `json:"timezone"`
	Currency         string    `json:"currency"`
	LandingHeadline  string    `json:"landing_headline"` // "" = the console's default copy
	LandingTagline   string    `json:"landing_tagline"`
	BudgetWebhookURL string    `json:"budget_webhook_url"` // "" = crossings not delivered
	UpdatedAt        time.Time `json:"updated_at"`
}

const settingsCols = `server_name, server_icon, timezone, currency, landing_headline, landing_tagline, budget_webhook_url, updated_at`

func scanSettings(r scanner) (*Settings, error) {
	var st Settings
	err := r.Scan(&st.ServerName, &st.ServerIcon, &st.Timezone, &st.Currency, &st.LandingHeadline, &st.LandingTagline, &st.BudgetWebhookURL, &st.UpdatedAt)
	return &st, err
}

func (s *Store) GetSettings(ctx context.Context) (*Settings, error) {
	return scanSettings(s.pool.QueryRow(ctx, `SELECT `+settingsCols+` FROM server_settings WHERE id=1`))
}

// UpdateSettings saves server_settings and, in the SAME transaction (#27),
// reschedules every live daily/monthly budget window for the new timezone:
// budget_reset_at moves to next_budget_reset(budget_type) for api_keys and
// groups alike. Without this, a saved timezone only took effect one window
// later — every existing reset instant stayed pinned to the old zone's
// midnight, and the console hint claimed otherwise.
//
// Only the reset INSTANT moves: spend_nanos is untouched, so the window in
// flight carries its spend over rather than restarting at zero. 'total' and
// 'unlimited' have a NULL budget_reset_at by definition and are excluded.
//
// The `budget_reset_at > now()` gate excludes due windows, and the direction
// of that reasoning matters — do NOT "simplify" it away. A due window
// (budget_reset_at <= now()) belongs to ResetBudgets or RecordSpend's inline
// roll, which advance the window AND zero spend_nanos together as ONE
// operation. The gate is the exact complement of that path's condition, so
// every row is claimed by exactly one side: no gap, no double-claim.
//
// Rescheduling a due row here would move the reset instant forward while
// leaving its spend_nanos alone — and that combination is STRICTER, not more
// forgiving. Limits.EffectiveSpend (repo.go) returns 0 for a windowed budget
// whose budget_reset_at is already past, precisely because the reset has not
// run yet (#21). Pushing the reset into the future would make the row look live
// again, so EffectiveSpend would suddenly start returning the stale mid-reset
// counter, and an already-expired window would turn back ON. On the real
// request path that means a spurious 429: the admit check in core/api/admit.go
// (`EffectiveSpend(now)+prior+estCost > BudgetNanos`) rejects with
// budget_exceeded until the reset worker runs. (Limits.OverBudget states the
// same predicate but is only used by tests, so name the admit path when
// reasoning about user-visible impact.) Nothing in this path zeroes spend, so
// no overspend is ever forgiven here; the gate avoids resurrecting a discarded
// counter that the reset path is about to replace wholesale.
//
// Rescheduling can also move a window EARLIER, shortening it: UTC's next
// midnight vs Asia/Taipei's is -8h, and against Pacific/Kiritimati (UTC+14) a
// window with ~20h left can end in ~7h. spend_nanos is preserved, so a scope
// carrying accrued spend into a now-shorter window can reach 80%/100% much
// sooner than it otherwise would. That is inherent to re-anchoring a window on
// a timezone change and is accepted — the alternative (zeroing spend to match)
// would destroy real spend. Callers' reports are not adjusted for it.
//
// next_budget_reset() is STABLE and reads server_settings, so it is evaluated
// against the timezone this transaction just wrote. That is load-bearing and
// not obvious: the recompute runs after the UPDATE above in the same tx, and a
// STABLE function takes the statement's snapshot. Command-counter
// invalidation covers exactly this case (a function reading a table the same
// statement already wrote), and TestUpdateSettings_ReschedulesBudgetsInNewTimezone
// is written so it fails if this ever reverts to the pre-update snapshot.
func (s *Store) UpdateSettings(ctx context.Context, in Settings) (*Settings, error) {
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return nil, fmt.Errorf("invalid timezone %q", in.Timezone)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	st, err := scanSettings(tx.QueryRow(ctx, `
		UPDATE server_settings SET server_name=$1, server_icon=$2, timezone=$3, currency=$4,
			landing_headline=$5, landing_tagline=$6, budget_webhook_url=$7, updated_at=now()
		WHERE id=1 RETURNING `+settingsCols,
		in.ServerName, in.ServerIcon, in.Timezone, in.Currency, in.LandingHeadline, in.LandingTagline, in.BudgetWebhookURL))
	if err != nil {
		return nil, err
	}

	for _, table := range []string{"api_keys", "groups"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET budget_reset_at = next_budget_reset(budget_type)
			WHERE budget_type IN ('daily','monthly') AND budget_reset_at > now()`); err != nil {
			return nil, fmt.Errorf("reschedule %s budgets: %w", table, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return st, nil
}
