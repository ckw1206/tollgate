package cache

import (
	"context"
	"testing"
)

// Tests for the in-flight reservation primitives behind #23 and #28.

func TestReserve_AccumulatesAndReportsPriorTotal(t *testing.T) {
	// Reserve reports the total held BEFORE this call (so the caller can add
	// its own estimate); the stored value is what accumulates.
	c, _ := newTest(t)
	ctx := context.Background()

	prior, _, err := c.Reserve(ctx, "key", "k1", 100, 0)
	if err != nil || prior != 0 {
		t.Fatalf("first reserve: prior=%d err=%v", prior, err)
	}
	prior, _, err = c.Reserve(ctx, "key", "k1", 50, 0)
	if err != nil || prior != 100 {
		t.Fatalf("second reserve should report prior=100: prior=%d err=%v", prior, err)
	}
	if stored, _, _ := c.Reserved(ctx, "key", "k1"); stored != 150 {
		t.Fatalf("stored reservation should accumulate to 150, got %d", stored)
	}
}

func TestReserve_ScopesAreIndependent(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	// A fresh scope has nothing prior, even though another scope is holding 100.
	if prior, _, _ := c.Reserve(ctx, "group", "k1", 10, 0); prior != 0 {
		t.Fatalf("key and group must not share counters, got prior=%d", prior)
	}
	if stored, _, _ := c.Reserved(ctx, "group", "k1"); stored != 10 {
		t.Fatalf("group reservation should be 10, got %d", stored)
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

func TestSettle_ClearsInflightReservation(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 50)
	if err := c.Settle(ctx, "key", "k1", 100, 50); err != nil {
		t.Fatal(err)
	}
	// The reservation is dropped outright. The request's real cost is NOT held
	// against the cap (see Settle's comment): holding it would double-count once
	// the recorder commits, which measured out as floors(cap/est)-1 capacity.
	if cost, tpm, _ := c.Reserved(ctx, "key", "k1"); cost != 0 || tpm != 0 {
		t.Fatalf("reservation should clear on settle, got cost=%d tpm=%d", cost, tpm)
	}
	if h, _, _ := c.Held(ctx, "key", "k1"); h != 0 {
		t.Fatalf("nothing should remain held after settle, got %d", h)
	}
}

func TestSettle_OnlyClearsThisRequestsEstimate(t *testing.T) {
	// Settling one request must not free another request's reservation on the
	// same scope.
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0) // request A (in flight)
	c.Reserve(ctx, "key", "k1", 30, 0)  // request B (in flight)
	if err := c.Settle(ctx, "key", "k1", 30, 0); err != nil {
		t.Fatal(err)
	}
	if cost, _, _ := c.Reserved(ctx, "key", "k1"); cost != 100 {
		t.Fatalf("request A's reservation must survive B's settle: got %d, want 100", cost)
	}
}

func TestHeld_IsTheInflightReservation(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()

	c.Reserve(ctx, "key", "k1", 100, 0)
	c.Reserve(ctx, "key", "k1", 25, 0)
	h, _, _ := c.Held(ctx, "key", "k1")
	if h != 125 {
		t.Fatalf("Held should be the summed in-flight reservations 125, got %d", h)
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

func TestReserve_ReturnsPriorTotalExcludingThisRequest(t *testing.T) {
	// The caller compares committed + prior + thisEstimate against the cap, so
	// the returned total must EXCLUDE the estimate just added. Returning the
	// post-increment value charged the request twice and made capacity
	// floor(cap/est)-1 instead of floor(cap/est).
	c, _ := newTest(t)
	ctx := context.Background()

	prior, _, err := c.Reserve(ctx, "key", "k1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if prior != 0 {
		t.Fatalf("first reserve must report prior=0, got %d", prior)
	}
	prior, _, err = c.Reserve(ctx, "key", "k1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if prior != 100 {
		t.Fatalf("second reserve must report prior=100 (the first only), got %d", prior)
	}
}

func TestSettle_RepeatedSettleDoesNotAccumulate(t *testing.T) {
	// Settling must be a subtraction of the reservation, never an accumulation
	// of cost. An earlier design accumulated each request's actual cost into a
	// separate "pending" key, which double-counted every committed request and
	// shrank effective capacity to floor(cap/estimate)-1.
	c, _ := newTest(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, _, err := c.Reserve(ctx, "key", "k1", 100, 0); err != nil {
			t.Fatal(err)
		}
		if err := c.Settle(ctx, "key", "k1", 100, 0); err != nil {
			t.Fatal(err)
		}
	}
	held, _, err := c.Held(ctx, "key", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Fatalf("three settled requests must leave nothing held, got %d", held)
	}
}

func TestReserved_MissingKeyIsZero(t *testing.T) {
	c, _ := newTest(t)
	cost, tpm, err := c.Reserved(context.Background(), "key", "never-used")
	if err != nil || cost != 0 || tpm != 0 {
		t.Fatalf("missing reservation should be zero, got %d/%d err=%v", cost, tpm, err)
	}
}
