package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// In-flight reservations (#23, #28).
//
// admit() reads a committed budget/TPM counter, but spend is only recorded in
// finish(), after upstream completes — which can be minutes for a stream
// (cmd/tollgate/main.go has no WriteTimeout). N concurrent requests therefore
// all read the same pre-spend counter and all pass, overshooting the cap by the
// sum of everything in flight.
//
// A reservation is written at admit and settled in finish(). Both a budget
// (nano-USD) and a TPM (tokens) reservation live in ONE hash per scope, so
// settling is a single round trip and the two can't drift apart.
//
// The TTL is a leak guard, not a reaper. A crash between reserve and settle
// must not pin a scope's budget forever, so every write refreshes a 30m expiry.
// It is deliberately generous — a reservation must outlive the longest possible
// request, or a still-running request would lose its reservation and admit a
// second one. upstreamTimeout (10m) bounds every upstream call, so 30m has
// headroom.
//
// The TTL cannot be tightened into a precise reaper for a busy scope, because
// the key is shared: EXPIRE must be refreshed on every write or a scope under
// steady traffic would drop its LIVE reservations mid-flight, which is far
// worse than a stale one. Measured: a leaked amount on an active scope is
// re-armed from 25m back to 30m by unrelated traffic. So a crash strands that
// request's estimate for as long as the scope stays busy, and it is only
// reclaimed once traffic stops.
//
// The blast radius is one estimate per crashed in-flight request (or the real
// cost if the crash happened after settle), never the scope's whole budget —
// the counter is a sum, and healthy requests settle normally. A Redis restart
// clears it outright, since nothing here is persisted.
const reserveTTL = 30 * time.Minute

// reserveKey is NOT window-scoped like rpmKey/tpmKey: a reservation spans
// arbitrary wall time (a stream started at 11:59:50 is still in flight at
// 12:00:01) and is not tied to a calendar window. The budget window it was
// admitted against is tracked separately by the caller via budget_reset_at.
func reserveKey(scope, id string) string {
	return fmt.Sprintf("rl:reserve:%s:%s", scope, id)
}

const (
	reserveCostField = "cost_nanos"
	reserveTPMField  = "tpm"
)

// Reserve atomically adds an estimate to a scope's in-flight reservation and
// returns the amount that was already reserved BEFORE this call, excluding the
// estimate just added.
//
// Returning the prior total (not the post-increment one) is deliberate: the
// caller must compare `committed + prior + thisEstimate` against the cap, and a
// self-inclusive return invites charging this request twice. An earlier version
// returned the inclusive total and the caller added its estimate again, making
// capacity floor(cap/estimate)-1 instead of floor(cap/estimate).
//
// Atomicity matters: reading the counter and deciding in the caller would
// reintroduce exactly the check-then-act race this exists to close. HINCRBY is
// atomic, so concurrent reservers serialize on the key.
func (c *Cache) Reserve(ctx context.Context, scope, id string, costNanos, tokens int64) (cost, tpm int64, err error) {
	k := reserveKey(scope, id)
	pipe := c.rdb.TxPipeline()
	// HINCRBY returns the post-increment value; subtract this call's own
	// contribution to report the prior total.
	costCmd := pipe.HIncrBy(ctx, k, reserveCostField, costNanos)
	tpmCmd := pipe.HIncrBy(ctx, k, reserveTPMField, tokens)
	pipe.Expire(ctx, k, reserveTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, 0, err
	}
	return costCmd.Val() - costNanos, tpmCmd.Val() - tokens, nil
}

// Release drops a reservation. Amounts are subtracted rather than the key being
// deleted: other requests may hold reservations on the same scope, so deleting
// would silently free their budget too. A negative result is clamped to zero by
// SettleReservation/ReleaseReservation callers via Release's own guard below.
func (c *Cache) Release(ctx context.Context, scope, id string, costNanos, tokens int64) error {
	if costNanos == 0 && tokens == 0 {
		return nil
	}
	k := reserveKey(scope, id)
	pipe := c.rdb.TxPipeline()
	pipe.HIncrBy(ctx, k, reserveCostField, -costNanos)
	pipe.HIncrBy(ctx, k, reserveTPMField, -tokens)
	// Keep the leak guard alive while other reservations may still be settling.
	pipe.Expire(ctx, k, reserveTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// Settle removes a finished request's in-flight reservation.
//
// The reservation is released outright rather than converted into a
// settled-but-uncommitted "hold". An earlier version kept the actual cost
// counting against the cap until the async recorder committed it (or a TTL
// expired), to bridge the recorder's 2s flush interval. That was removed
// because it cannot be made correct without a completion signal from the
// recorder (Record is fire-and-forget, so nothing can tell Redis the commit
// landed), and the error it introduced was worse than the one it fixed:
//
//   - It double-counted every committed request for the hold's lifetime (the
//     cost sits in both spend_nanos and the hold), so a budget of exactly N
//     request-costs admitted only N-1. Measured, not theoretical.
//   - It could only ever be reconciled by a TTL guess, so tuning it meant
//     trading permanent under-admission against a brief over-admission.
//
// What is left is an honest, bounded gap: between finish() and the recorder's
// next flush (<= 2s), a completed request's cost is in neither the reservation
// nor spend_nanos. That window is small, self-closing, and far narrower than
// the original bug, where a request was uncounted for its entire duration —
// minutes, for a stream.
//
// Single Lua script so the deltas can't interleave with another Reserve.
var settleScript = redis.NewScript(`
local key = KEYS[1]
local costDelta = tonumber(ARGV[1])
local tpmDelta = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])

local cost = redis.call('HINCRBY', key, 'cost_nanos', costDelta)
local tpm = redis.call('HINCRBY', key, 'tpm', tpmDelta)

if cost <= 0 and tpm <= 0 then
  redis.call('DEL', key)
else
  redis.call('EXPIRE', key, ttl)
end
return {cost, tpm}
`)

// Settle removes a scope's in-flight reservation. The deltas are full
// negatives: the reservation is cleared, not adjusted. This is a release, not a
// reconciliation — the request's real cost is committed to spend_nanos by the
// recorder and deliberately not held here (see the note above).
func (c *Cache) Settle(ctx context.Context, scope, id string, reservedCost, reservedTokens int64) error {
	return settleScript.Run(ctx, c.rdb, []string{reserveKey(scope, id)},
		-reservedCost, -reservedTokens, int64(reserveTTL/time.Second)).Err()
}

// Reserved returns the current in-flight reservation for a scope, for display
// in the console. A missing key is zero, not an error.
func (c *Cache) Reserved(ctx context.Context, scope, id string) (cost, tpm int64, err error) {
	vs, err := c.rdb.HMGet(ctx, reserveKey(scope, id), reserveCostField, reserveTPMField).Result()
	if err != nil {
		return 0, 0, err
	}
	return hashInt(vs[0]), hashInt(vs[1]), nil
}

// Held returns what must count against a cap but is not yet in spend_nanos:
// the live in-flight reservations. This is the number admission compares with.
//
// There is deliberately no "settled but uncommitted" term: a finished request's
// cost is not held, because nothing can clear such a hold when the recorder
// commits (see Settle).
func (c *Cache) Held(ctx context.Context, scope, id string) (cost, tpm int64, err error) {
	return c.Reserved(ctx, scope, id)
}

// hashInt converts an HMGET element (nil when the field is absent) to int64,
// clamping negatives — an over-release must never hand back budget.
func hashInt(v any) int64 {
	var n int64
	switch t := v.(type) {
	case nil:
		return 0
	case string:
		fmt.Sscan(t, &n)
	case int64:
		n = t
	}
	if n < 0 {
		return 0
	}
	return n
}
