package repo_test

import (
	"context"
	"testing"
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
	if _, err := s.UpdateSettings(ctx, *cur); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}
