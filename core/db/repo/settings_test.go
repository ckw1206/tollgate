package repo_test

import (
	"context"
	"strings"
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
