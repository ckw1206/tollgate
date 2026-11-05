package repo

import (
	"context"
	"errors"
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

// ErrInvalidTimezone: the timezone is empty or unknown to this host's tzdata.
var ErrInvalidTimezone = errors.New("invalid timezone")

// UpdateSettings saves the settings. When the timezone changes, every
// daily/monthly window is rescheduled to its next boundary in the NEW zone, in
// the same transaction (#27): next_budget_reset reads server_settings.timezone,
// so without this the stored budget_reset_at values keep the old zone's
// boundary until each row next resets — up to a full window later.
//
// Each counter becomes the scope's spend logged since the current local day or
// month start in the new zone: the window it is now in, by the same rule
// RecordSpend uses (Limits.WindowStart / inWindowCosts). The zones' windows
// don't line up, so a counter carried over as-is would be wrong either way: a
// change late in a window (UTC→Taipei on Nov 30 at 20:00 UTC, a Taipei month
// that began 4 hours ago) would charge November against December and could
// refuse a key for a month. Any rule that adjusts the old counter instead
// depends on the path: switching zones back and forth would drain or inflate
// it. A recount doesn't: the result depends only on the zone saved.
//
// Known approximations, all bounded by the spend in the new window and only on
// a zone change. A key rotated within the window counts only its own logs: the
// counter it inherited from its predecessor isn't linked to it. A budget-type
// change within the window counts the logs from before the change. A recount
// that jumps past 80% or 100% sends no crossing alert, because detectCrossings
// fires on writes only (admit() still enforces the cap). A key or group created
// or edited at the moment the save commits can keep one old-zone boundary; it
// aligns at its next reset. With SPEND_RETENTION_DAYS shorter than a month, a
// monthly recount covers only the retained logs.
//
// Lock order: the settings row first (FOR UPDATE here, FOR SHARE in
// RecordSpend), then key and group rows. A spend batch either commits before
// this save takes its snapshot, or waits and then sees the new zone, so
// neither overwrites the other's counter.
func (s *Store) UpdateSettings(ctx context.Context, in Settings) (*Settings, error) {
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return nil, fmt.Errorf("%w %q", ErrInvalidTimezone, in.Timezone)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var oldTZ string
	if err := tx.QueryRow(ctx, `SELECT timezone FROM server_settings WHERE id=1 FOR UPDATE`).Scan(&oldTZ); err != nil {
		return nil, err
	}
	st, err := scanSettings(tx.QueryRow(ctx, `
		UPDATE server_settings SET server_name=$1, server_icon=$2, timezone=$3, currency=$4,
			landing_headline=$5, landing_tagline=$6, budget_webhook_url=$7, updated_at=now()
		WHERE id=1 RETURNING `+settingsCols,
		in.ServerName, in.ServerIcon, in.Timezone, in.Currency, in.LandingHeadline, in.LandingTagline, in.BudgetWebhookURL))
	if err != nil {
		return nil, err
	}
	if oldTZ != in.Timezone {
		// A later statement in this transaction sees the new timezone, so
		// next_budget_reset computes boundaries in the new zone, and the window
		// start is the same boundary one period earlier: the current local day
		// or month start ($1 = the new zone).
		for _, sc := range []struct{ table, col string }{{"api_keys", "key_id"}, {"groups", "group_id"}} {
			if _, err := tx.Exec(ctx, `UPDATE `+sc.table+` t SET
				budget_reset_at = next_budget_reset(t.budget_type),
				spend_nanos = COALESCE((SELECT sum(l.cost_nanos) FROM spend_logs l
					WHERE l.`+sc.col+` = t.id AND l.created_at >= date_trunc(
						CASE t.budget_type WHEN 'daily' THEN 'day' ELSE 'month' END, now() AT TIME ZONE $1) AT TIME ZONE $1), 0)::bigint
				WHERE t.budget_type IN ('daily','monthly')`, in.Timezone); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return st, nil
}
