package cache

import (
	"context"
	"errors"
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
// The TTL is the leak guard: a crash between reserve and settle must not pin a
// scope's budget forever. It is deliberately generous — a reservation must
// outlive the longest possible request, or a still-running request would lose
// its reservation and admit a second one.
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
// returns the totals that count against the cap: the new in-flight reservation
// PLUS any settled-but-not-yet-committed cost (see Settle).
//
// Atomicity matters: reading the counter and deciding in the caller would
// reintroduce exactly the check-then-act race this exists to close. HINCRBY is
// atomic, so concurrent reservers serialize on the key.
func (c *Cache) Reserve(ctx context.Context, scope, id string, costNanos, tokens int64) (cost, tpm int64, err error) {
	k := reserveKey(scope, id)
	pipe := c.rdb.TxPipeline()
	costCmd := pipe.HIncrBy(ctx, k, reserveCostField, costNanos)
	tpmCmd := pipe.HIncrBy(ctx, k, reserveTPMField, tokens)
	pipe.Expire(ctx, k, reserveTTL)
	pendingCmd := pipe.Get(ctx, k+":pending")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, 0, err
	}
	cost = costCmd.Val()
	if pv, perr := pendingCmd.Result(); perr == nil {
		cost += hashInt(pv)
	} else if !errors.Is(perr, redis.Nil) {
		return 0, 0, perr
	}
	return cost, tpmCmd.Val(), nil
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

// Settle converts a finished request's in-flight reservation into a short-lived
// "pending commit" hold.
//
// The gap this closes: spend is committed to Postgres by an async recorder that
// flushes every 2s (spend.Recorder.FlushEvery), so between finish() and the
// commit a request's cost is visible to neither the reservation nor
// spend_nanos. Releasing outright would reopen a (much smaller) version of the
// overshoot this mechanism prevents.
//
// The in-flight estimate is removed ENTIRELY and the actual cost takes its
// place under a separate key with its own TTL. The two live on different clocks
// on purpose: in-flight must not outlive a request, while the pending hold must
// outlive a flush. Keeping them separate is what stops a settled request from
// being counted twice.
//
// Single Lua script so the deltas can't interleave with another Reserve.
var settleScript = redis.NewScript(`
local key = KEYS[1]
local costDelta = tonumber(ARGV[1])
local tpmDelta = tonumber(ARGV[2])
local pending = tonumber(ARGV[3])
local pendingTtl = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local cost = redis.call('HINCRBY', key, 'cost_nanos', costDelta)
local tpm = redis.call('HINCRBY', key, 'tpm', tpmDelta)

if pending > 0 and pendingTtl > 0 then
  redis.call('SET', key .. ':pending', pending, 'EX', pendingTtl)
end

if cost <= 0 and tpm <= 0 then
  redis.call('DEL', key)
else
  redis.call('EXPIRE', key, ttl)
end
return {cost, tpm}
`)

// pendingTTL is how long a settled-but-uncommitted cost keeps counting against
// the cap. It must exceed Recorder.FlushEvery (2s) plus a flush retry; 30s is
// comfortably inside that and still self-healing if a hold is orphaned.
const pendingTTL = 30 * time.Second

// Settle removes a scope's in-flight reservation (the estimate) and records the
// request's actual cost as a pending hold until the spend recorder commits.
// The deltas are full negatives: the reservation is cleared, not adjusted.
func (c *Cache) Settle(ctx context.Context, scope, id string, reservedCost, reservedTokens, actualCost, actualTokens int64) error {
	pending := actualCost
	if pending < 0 {
		pending = 0
	}
	return settleScript.Run(ctx, c.rdb, []string{reserveKey(scope, id)},
		-reservedCost, -reservedTokens,
		pending, int64(pendingTTL/time.Second), int64(reserveTTL/time.Second)).Err()
}

// Pending returns the settled-but-not-yet-committed cost for a scope, for
// display in the console alongside the in-flight reservation.
func (c *Cache) Pending(ctx context.Context, scope, id string) (int64, error) {
	v, err := c.rdb.Get(ctx, reserveKey(scope, id)+":pending").Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return hashInt(v), nil
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

// Held returns everything that must count against a cap but is not yet in
// spend_nanos: live in-flight reservations plus settled cost still waiting on
// the spend recorder's flush. This is the number admission compares with.
func (c *Cache) Held(ctx context.Context, scope, id string) (cost, tpm int64, err error) {
	k := reserveKey(scope, id)
	pipe := c.rdb.Pipeline()
	hash := pipe.HMGet(ctx, k, reserveCostField, reserveTPMField)
	pending := pipe.Get(ctx, k+":pending")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, 0, err
	}
	vs := hash.Val()
	pv, perr := pending.Result()
	if perr != nil && !errors.Is(perr, redis.Nil) {
		return 0, 0, perr
	}
	return hashInt(vs[0]) + hashInt(pv), hashInt(vs[1]), nil
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
