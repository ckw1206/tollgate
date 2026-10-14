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

func (s *Store) UpdateSettings(ctx context.Context, in Settings) (*Settings, error) {
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return nil, fmt.Errorf("invalid timezone %q", in.Timezone)
	}
	return scanSettings(s.pool.QueryRow(ctx, `
		UPDATE server_settings SET server_name=$1, server_icon=$2, timezone=$3, currency=$4,
			landing_headline=$5, landing_tagline=$6, budget_webhook_url=$7, updated_at=now()
		WHERE id=1 RETURNING `+settingsCols,
		in.ServerName, in.ServerIcon, in.Timezone, in.Currency, in.LandingHeadline, in.LandingTagline, in.BudgetWebhookURL))
}
