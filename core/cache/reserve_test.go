package cache

import (
	"context"
	"testing"
	"time"
)

// Tests for the in-flight reservation primitives behind #23 and #28.

func TestReserve_AccumulatesAndReturnsTotals(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	cost, _, err := c.Reserve(ctx, "key", "k1", 100, 0)
	if err != nil || cost != 100 {
		t.Fatalf("first reserve: cost=%d err=%v", cost, err)
	}
	cost, _, err = c.Reserve(ctx, "key", "k1", 50, 0)
	if err != nil || cost != 150 {
		t.Fatalf("second reserve should accumulate: cost=%d err=%v", cost, err)
	}
}

func TestReserve_ScopesAreIndependent(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	if cost, _, _ := c.Reserve(ctx, "group", "k1", 10, 0); cost != 10 {
		t.Fatalf("key and group must not share counters, got %d", cost)
	}
}

func TestRelease_SubtractsAndClampsToZero(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	if err := c.Release(ctx, "key", "k1", 30, 0); err != nil {
		t.Fatal(err)
	}
	if cost, _, _ := c.Reserved(ctx, "key", "k1"); cost != 70 {
		t.Fatalf("expected 70 after releasing 30, got %d", cost)
	}
	// An over-release must never hand back budget as a negative.
	c.Release(ctx, "key", "k1", 1000, 0)
	if cost, _, _ := c.Reserved(ctx, "key", "k1"); cost != 0 {
		t.Fatalf("over-release must clamp to zero, got %d", cost)
	}
}

func TestSettle_ClearsInflightAndHoldsActualOnce(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 50)
	if err := c.Settle(ctx, "key", "k1", 100, 50, 120, 60); err != nil {
		t.Fatal(err)
	}
	// The in-flight estimate must be gone entirely: nothing is in flight now.
	if cost, tpm, _ := c.Reserved(ctx, "key", "k1"); cost != 0 || tpm != 0 {
		t.Fatalf("in-flight should clear on settle, got cost=%d tpm=%d", cost, tpm)
	}
	if p, _ := c.Pending(ctx, "key", "k1"); p != 120 {
		t.Fatalf("pending should be the actual cost 120, got %d", p)
	}
	// Held must count the actual once, not once per counter.
	if h, _, _ := c.Held(ctx, "key", "k1"); h != 120 {
		t.Fatalf("Held must not double-count: got %d, want 120", h)
	}
}

func TestSettle_UnderestimateHoldsTheRealCost(t *testing.T) {
	// A request that costs more than it reserved must raise the hold to the
	// real figure, not leave the lower estimate behind.
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	c.Settle(ctx, "key", "k1", 100, 0, 500, 0)
	if p, _ := c.Pending(ctx, "key", "k1"); p != 500 {
		t.Fatalf("pending should be the real cost 500, got %d", p)
	}
}

func TestHeld_CountsReservationAndPendingTogether(t *testing.T) {
	// The admission comparison must see a concurrent reservation AND a
	// not-yet-committed settle at the same time.
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	c.Settle(ctx, "key", "k1", 100, 0, 100, 0) // becomes pending
	c.Reserve(ctx, "key", "k1", 25, 0)         // a new request in flight
	h, _, _ := c.Held(ctx, "key", "k1")
	if h != 125 {
		t.Fatalf("Held should be pending 100 + in-flight 25 = 125, got %d", h)
	}
}

func TestReserve_TTLIsSetAsLeakGuard(t *testing.T) {
	// A crash between reserve and settle must not pin the budget forever.
	c, mr := newTest(t)
	ctx := context.Background()
	c.Reserve(ctx, "key", "k1", 100, 0)
	if ttl := mr.TTL(reserveKey("key", "k1")); ttl <= 0 || ttl > reserveTTL {
		t.Fatalf("reservation must carry a TTL, got %v", ttl)
	}
}

func TestPending_ExpiresOnItsOwn(t *testing.T) {
	// The pending hold is the self-healing part: if the spend recorder never
	// commits, the hold must age out rather than pin the scope.
	c, mr := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	c.Settle(ctx, "key", "k1", 100, 0, 100, 0)
	if p, _ := c.Pending(ctx, "key", "k1"); p != 100 {
		t.Fatalf("expected a pending hold, got %d", p)
	}
	mr.FastForward(pendingTTL + time.Second)
	if p, _ := c.Pending(ctx, "key", "k1"); p != 0 {
		t.Fatalf("pending hold should expire, got %d", p)
	}
	if h, _, _ := c.Held(ctx, "key", "k1"); h != 0 {
		t.Fatalf("nothing should be held after expiry, got %d", h)
	}
}

func TestReserved_MissingKeyIsZero(t *testing.T) {
	c, _ := newTest(t)
	cost, tpm, err := c.Reserved(context.Background(), "key", "never-used")
	if err != nil || cost != 0 || tpm != 0 {
		t.Fatalf("missing reservation should be zero, got %d/%d err=%v", cost, tpm, err)
	}
}
