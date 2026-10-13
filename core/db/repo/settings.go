package repo

import (
	"context"
	"fmt"
	"time"
)

type Settings struct {
	ServerName string    `json:"server_name"`
	ServerIcon string    `json:"server_icon"`
	Timezone   string    `json:"timezone"`
	Currency   string    `json:"currency"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (s *Store) GetSettings(ctx context.Context) (*Settings, error) {
	var st Settings
	err := s.pool.QueryRow(ctx, `SELECT server_name, server_icon, timezone, currency, updated_at FROM server_settings WHERE id=1`).
		Scan(&st.ServerName, &st.ServerIcon, &st.Timezone, &st.Currency, &st.UpdatedAt)
	return &st, err
}

func (s *Store) UpdateSettings(ctx context.Context, in Settings) (*Settings, error) {
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return nil, fmt.Errorf("invalid timezone %q", in.Timezone)
	}
	var st Settings
	err := s.pool.QueryRow(ctx, `
		UPDATE server_settings SET server_name=$1, server_icon=$2, timezone=$3, currency=$4, updated_at=now()
		WHERE id=1 RETURNING server_name, server_icon, timezone, currency, updated_at`,
		in.ServerName, in.ServerIcon, in.Timezone, in.Currency).
		Scan(&st.ServerName, &st.ServerIcon, &st.Timezone, &st.Currency, &st.UpdatedAt)
	return &st, err
}
