package consoleapi

import (
	"context"
	"testing"

	"tollgate/core/db/repo"
)

// Tests for surfacing in-flight reservations in the console (#23).
//
// The reservation lives in Redis, not Postgres, so it is not part of the row
// read from the database. These pin that the list endpoints populate it.

func TestListKeys_IncludesReservedNanos(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()

	_, key, err := e.store.CreateKey(ctx, e.member.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "daily", BudgetNanos: 1_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	// A request in flight, plus a settled-but-uncommitted cost.
	if _, _, err := e.cache.Reserve(ctx, "key", key.ID, 300_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.cache.Settle(ctx, "key", key.ID, 0, 0, 200_000_000, 0); err != nil {
		t.Fatal(err)
	}

	keys := decodeInto[[]repo.APIKey](t, e.as("admin@x.io", "GET", p+"/keys", nil))
	var got *repo.APIKey
	for i := range keys {
		if keys[i].ID == key.ID {
			got = &keys[i]
		}
	}
	if got == nil {
		t.Fatal("key not in listing")
	}
	if got.ReservedNanos != 500_000_000 {
		t.Fatalf("reserved_nanos should be in-flight 300M + pending 200M = 500M, got %d", got.ReservedNanos)
	}
}

func TestListKeys_UnlimitedBudgetHasNoReservation(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()

	_, key, err := e.store.CreateKey(ctx, e.member.ID, repo.KeyInput{Alias: "k", GroupID: e.group.ID, BudgetType: "unlimited"})
	if err != nil {
		t.Fatal(err)
	}
	// Even if a reservation somehow existed, an unlimited budget has no cap to
	// reserve against, so there is nothing meaningful to show.
	if _, _, err := e.cache.Reserve(ctx, "key", key.ID, 300_000_000, 0); err != nil {
		t.Fatal(err)
	}

	keys := decodeInto[[]repo.APIKey](t, e.as("admin@x.io", "GET", p+"/keys", nil))
	for _, k := range keys {
		if k.ID == key.ID && k.ReservedNanos != 0 {
			t.Fatalf("unlimited budget should report no reservation, got %d", k.ReservedNanos)
		}
	}
}

func TestListGroups_IncludesReservedNanos(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()

	if _, err := e.store.UpdateGroup(ctx, e.group.ID,
		repo.GroupInput{Name: "default", BudgetType: "monthly", BudgetNanos: 10_000_000_000}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.cache.Reserve(ctx, "group", e.group.ID, 2_500_000_000, 0); err != nil {
		t.Fatal(err)
	}

	groups := decodeInto[[]repo.Group](t, e.as("admin@x.io", "GET", p+"/groups", nil))
	var found bool
	for _, g := range groups {
		if g.ID == e.group.ID {
			found = true
			if g.ReservedNanos != 2_500_000_000 {
				t.Fatalf("group reserved_nanos = %d, want 2500000000", g.ReservedNanos)
			}
		}
	}
	if !found {
		t.Fatal("group not in listing")
	}
}

func TestGate_IncludesReservedNanos(t *testing.T) {
	e := newTenv(t)
	ctx := context.Background()

	if _, err := e.store.UpdateGroup(ctx, e.group.ID,
		repo.GroupInput{Name: "default", BudgetType: "daily", BudgetNanos: 5_000_000_000}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.cache.Reserve(ctx, "group", e.group.ID, 1_250_000_000, 0); err != nil {
		t.Fatal(err)
	}

	got := decodeInto[gateReservedResp](t, e.as("member@x.io", "GET", p+"/gate", nil))
	if got.Budget == nil {
		t.Fatal("expected a budget block")
	}
	if got.Budget.ReservedNanos != 1_250_000_000 {
		t.Fatalf("gate reserved_nanos = %d, want 1250000000", got.Budget.ReservedNanos)
	}
}

type gateReservedResp struct {
	Budget *struct {
		Type          string `json:"type"`
		BudgetNanos   int64  `json:"budget_nanos"`
		SpendNanos    int64  `json:"spend_nanos"`
		ReservedNanos int64  `json:"reserved_nanos"`
	} `json:"budget"`
}
