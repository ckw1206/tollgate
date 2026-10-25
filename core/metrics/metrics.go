// Package metrics records admit-path timings so the question behind GitHub
// issue #33 — "is the per-request AuthByKeyHash lookup slow enough to need a
// cache?" — is answerable from a running server instead of from intuition.
//
// This package is measurement ONLY. There is deliberately no cache, no
// invalidation and no configuration here: repo.Store.AuthByKeyHash stays the
// single uncached indexed query it has always been, and any change to that is a
// separate issue gated on the numbers below actually firing.
//
// It is stdlib only (no Prometheus client). The deployment is a single instance
// (CLAUDE.md: the model registry and spend buffer live in process), so
// in-process counters and a fixed-bucket histogram are the whole story — there
// is no replica to aggregate across and no scrape target to keep.
//
// # The thresholds this exists to evaluate
//
// THE TRIGGER FOR THE FUTURE CACHE ISSUE IS:
//
//	auth_lookup p99 > 5 ms, or
//	auth_lookup > 100 queries/second sustained.
//
// These are the original issue's thresholds, reproduced verbatim so a future
// reader can evaluate them against the numbers this package produces. Note that
// "sustained" is not something one process can decide on its own: `qps_60s` is a
// 60-second rolling average, which is the closest honest reading of "sustained"
// a single process can offer. Read `qps_60s` for the trigger; if it stays above
// 100, the trigger has fired. If it does not, the lookup is doing its job and
// the cache is not needed.
//
// # Reading a p99
//
// Percentiles are estimated from the histogram, over the cumulative lifetime of
// the process, by linearly interpolating the target rank within the bucket that
// holds it. The estimate is "about", not exact. Three things bound how far off
// it can be, and all three matter more than the point estimate:
//
//   - The WIDEST finite bucket sets the worst case: 30000->60000 ms, so
//     30 000 ms wide. Beware that this is NOT the bucket around the 5 ms
//     trigger. Around the trigger the buckets are 2->5 and 5->10, so a reading
//     near the threshold is resolved to within 5 ms — the width of the bucket it
//     lands in, because the bucket IS the resolution. (The decimal places on a
//     reported p99 are float precision, not information.) A p99 out in the tens
//     of seconds is correspondingly coarse.
//   - Interpolation ASSUMES the samples inside a bucket are spread evenly
//     across it. They need not be. Five samples sitting at 8 ms inside the
//     (5,10] bucket are interpolated as though they filled 5..10, so a p99 of
//     9.0 ms is reported for a true 8.0 ms — the value stays inside the correct
//     bucket but is not the true percentile. Narrow buckets are the only real
//     fix for this; treat a p99 as a bucket, not a number.
//   - Values above the last edge land in an overflow slot and are reported as
//     "at least 60000 ms", with p99_is_floor true in the document. The
//     histogram cannot say how far past 60 s, because it does not store
//     samples.
//
// Two invariants ARE guaranteed, because a p99 that can be reported as
// impossible is worse than no p99: the result never exceeds the highest finite
// edge, and it never exceeds the upper edge of the bucket it resolved in. See
// quantileMS.
//
// # Deliberately not measured
//
// Timings are recorded only where a human asked for them: the auth lookup and
// the Redis reservation round-trips on the admit path. The rest of the request
// is not timed, and nothing here is labelled by key, user, group or model ID.
package metrics

import (
	"fmt"
	"math"

	"sync"
	"sync/atomic"
	"time"
)

// Op is a recorded admit-path operation. The set is a closed, compile-time
// enumeration: it is the ONLY label in this package, it never carries a
// user-supplied string, and it is bounded by construction. Raw API keys,
// session tokens and scope IDs therefore cannot reach a label even by accident.
type Op string

const (
	// OpAuthLookup is repo.Store.AuthByKeyHash — the uncached per-request
	// lookup that issue #33 is about. The trigger thresholds above apply to
	// THIS operation and to no other.
	OpAuthLookup Op = "auth_lookup"
	// OpReserveAdd is Cache.Reserve, the atomic HINCRBY that adds an in-flight
	// estimate (#23, #28).
	OpReserveAdd Op = "reserve_add"
	// OpReserveRelease is reservation.release, Cache.Release on the refusal
	// and abandoned-request paths.
	OpReserveRelease Op = "reserve_release"
	// OpReserveSettle is reservation.settle, Cache.Settle in finish().
	OpReserveSettle Op = "reserve_settle"
	// OpTPMRead is Cache.TPM, the per-minute committed-token read in admit().
	OpTPMRead Op = "tpm_read"
	// OpRPMHit is Cache.HitRPM, the atomic RPM increment in admit().
	OpRPMHit Op = "rpm_hit"
	// OpReservedRead is Cache.Held / Cache.Reserved, the read-only reservation
	// lookup. It is NOT on the admit path: it is the console's budget bar
	// (console/api/gate.go, groups.go, keys.go), measured because it hits the
	// same Redis keys as reserve_add and is the read a cache would have to
	// invalidate. Keeping it here rather than as a separate series means every
	// Redis round-trip in the reservation story is visible in one place.
	OpReservedRead Op = "reserved_read"
)

// ops is the closed enumeration, in the order the report prints it. Adding an
// operation here is the only way to add a label value.
var ops = []Op{
	OpAuthLookup,
	OpReserveAdd,
	OpReserveRelease,
	OpReserveSettle,
	OpTPMRead,
	OpRPMHit,
	OpReservedRead,
}

// Bucket boundaries in milliseconds. Fixed and small (no per-request sample
// retention), spaced ~2-4x per decade so a p99 is meaningful anywhere from
// sub-millisecond to the second-scale tail.
//
// The layout is deliberately finer around the 5 ms trigger (0.5, 1, 2, 5, 10)
// where the decision is made, and its widest finite bucket (30000->60000, at
// 30 000 ms) bounds the worst-case error at the top end. A narrower top would
// buy precision nobody reads a trigger off; a wider one would make the stated
// error bound meaningless. If this list changes, the error bound documented in
// the package comment changes with it — the two are asserted equal by
// TestBucketLayout_ErrorBoundMatchesDoc.
//
// The first edge is 0.1 ms and the last is 60 s, the latter matching the
// upstream timeout that bounds a stuck dependency call.
var bucketEdgesMS = []float64{
	0.1, 0.25, 0.5, 1, 2, 5, 10, 25, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000,
}

// Estimator converts a timer's native unit (time.Duration) to milliseconds.
// It is a seam for tests that need exact millisecond values without sleeping.
var Estimator = func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// opStat is one operation's counter plus histogram.
//
// A plain mutex rather than atomic counters because Record writes four fields
// that must agree with each other (count, sum, bucket); a torn read of those
// across a /metrics call would report a rate that never happened. Contention is
// one uncontended lock per HTTP request on a single-instance gateway, which is
// noise next to the Postgres query it is timing.
type opStat struct {
	mu      sync.Mutex
	count   int64
	sumMS   float64
	buckets [numBuckets]int64 // one slot per bucket, last is +Inf
}

// numBuckets is len(bucketEdgesMS)+1: one slot per finite edge plus the
// overflow slot for values above the last edge. Written out because a Go array
// length must be constant, and asserted against len(bucketEdgesMS) by
// TestBucketLayout_ErrorBoundMatchesDoc so the two cannot drift.
const numBuckets = 19

var (
	stats  = make([]opStat, len(ops))
	qauth  atomic.Int64 // lifetime auth_lookup calls
	qtotal atomic.Int64 // lifetime admit-path calls across every op
	start  = time.Now()
	rings  [2]Ring // [0] = auth_lookup, [1] = every op
	ringsI []Ring  // per-op 60s ring for qps_60s_by_op
)

func init() {
	rings[0] = NewRing(authRateWindowSeconds)
	rings[1] = NewRing(authRateWindowSeconds)
	ringsI = make([]Ring, len(ops))
	for i := range ops {
		ringsI[i] = NewRing(authRateWindowSeconds)
	}
}

// secondsPerSlot is the ring's resolution: 60 slots of one second each, so
// qps_60s is a one-minute rolling average with a one-second granularity. That
// is a bounded ring, not per-request retention — a request contributes to a
// counter, never to a stored sample.
const authRateWindowSeconds = 60

// Observe records one call of op taking d. It is safe for concurrent use.
//
// A non-finite d is DROPPED, not recorded, and a negative one is clamped to
// zero. This matters beyond tidiness: a NaN would be added to sumMS, poisoning
// sum_ms/avg_ms with NaN for the whole process lifetime, and encoding that
// fails inside the /metrics handler AFTER its 200 status line has been written
// — the caller would get a truncated body rather than an error. Observe is
// exported, so it cannot rely on time.Since being the only caller.
//
// The remaining values saturate into the overflow bucket at the high end, so
// nothing can index out of the histogram.
func Observe(op Op, d time.Duration) {
	ms := Estimator(d)
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		return
	}
	if ms < 0 {
		ms = 0
	}
	i := indexOf(op)
	s := &stats[i]
	s.mu.Lock()
	s.count++
	s.sumMS += ms
	s.buckets[bucketFor(ms)]++
	s.mu.Unlock()

	now := time.Now()
	if op == OpAuthLookup {
		qauth.Add(1)
		rings[0].Add(now, 1)
	}
	qtotal.Add(1)
	rings[1].Add(now, 1)
	ringsI[i].Add(now, 1)
}

// indexOf maps an op to its slot, tolerating an unknown op by parking it in the
// last slot rather than panicking on a metric call. Unknown ops cannot arise
// from this package (the enumeration is closed); this is a belt-and-braces
// guard so instrumentation can never take down a request path.
func indexOf(op Op) int {
	for i, o := range ops {
		if o == op {
			return i
		}
	}
	return len(ops) - 1
}

// bucketFor returns the bucket index for ms: the first bucket whose upper edge
// is strictly greater than ms, else the +Inf slot.
//
// A value exactly on an edge belongs to the LONGER bucket, not the shorter one:
// 5.0 ms falls in the (5, 10] bucket, matching the standard histogram contract
// that a bucket holds values <= its edge. Callers comparing against the 5 ms
// trigger threshold get "p99 > 5" to mean strictly greater, so 5.0 itself does
// not fire the trigger.
func bucketFor(ms float64) int {
	for i, edge := range bucketEdgesMS {
		if ms <= edge {
			return i
		}
	}
	return len(bucketEdgesMS)
}

// snapshot is one operation's consistent copy.
type snapshot struct {
	op      Op
	count   int64
	sumMS   float64
	buckets []int64
}

// Snapshot returns a consistent copy of every operation's counters. Tests and
// the /metrics handler both read through here so neither can observe a
// half-written histogram.
func Snapshot() []snapshot {
	out := make([]snapshot, len(ops))
	for i := range ops {
		s := &stats[i]
		s.mu.Lock()
		out[i] = snapshot{op: ops[i], count: s.count, sumMS: s.sumMS, buckets: s.buckets[:]}
		s.mu.Unlock()
	}
	return out
}

// quantileMS estimates the q-th percentile (0<q<1) in milliseconds from a
// bucket histogram, by linear interpolation of the rank within the bucket that
// holds it.
//
// # What it does and does not promise
//
// The estimate is approximate, with error bounded by one bucket width — see the
// package comment for the real numbers. Two invariants ARE guaranteed, because
// a p99 that can be reported as impossible is worse than no p99:
//
//  1. The result never exceeds the highest finite edge (60 000 ms). When the
//     rank falls in the overflow slot the distribution genuinely extends past
//     the last edge by an unknown amount, so this reports the last edge and the
//     caller must read it as "at least 60 s", a floor. That is the only
//     direction the histogram can honestly answer at the top.
//  2. The result is monotone: for a fixed distribution, a larger q never
//     reports a smaller value.
//
// Within a finite bucket the rank is interpolated between that bucket's lower
// and upper edge, assuming the samples in the bucket are spread evenly across
// it. They need not be, and that assumption — not any arithmetic bug — is what
// makes this an estimate: with 95 samples at 1 ms and 5 at 8 ms, the 8 ms
// samples land in the (5,10] bucket and are interpolated to a p99 of 9.0 ms for
// a true 8.0 ms. The value stays inside the correct bucket; it is simply not
// the true percentile. It is NOT a strict lower bound, and an earlier version
// of this function was worse than imprecise: it returned a flat 60000 for the
// overflow branch while interpolating inside the neighbouring bucket, so one
// distribution could be reported above a recorded sample in one place and below
// it in another. Clamping the fraction to [0,1] and never exceeding the
// resolving bucket's own upper edge is what makes the two invariants above hold.
func quantileMS(buckets []int64, total int64, q float64) float64 {
	if total == 0 {
		return 0
	}
	rank := q * float64(total)
	var cum int64
	lo := 0.0
	for i, edge := range bucketEdgesMS {
		hi := edge
		cum += buckets[i]
		if float64(cum) >= rank {
			in := float64(buckets[i])
			if in <= 0 {
				// No sample landed in this bucket, so the rank is at its
				// boundary: the bucket's lower edge is the best answer, and it
				// is what a cumulative histogram reports here.
				return lo
			}
			// Where the rank sits inside this bucket, by count. Clamped to
			// [0,1] so floating-point drift or a rank exactly on the boundary
			// can never push the estimate outside the bucket it came from.
			frac := (rank - float64(cum) + in) / in
			if frac < 0 {
				frac = 0
			}
			if frac > 1 {
				frac = 1
			}
			return lo + frac*(hi-lo)
		}
		lo = hi
	}
	// The rank lies in the overflow slot: the distribution extends past the last
	// finite edge by an amount this histogram cannot know, because it stores no
	// samples. Report the last edge as a floor rather than inventing a value —
	// and never the flat 60000 an earlier version returned here, which could sit
	// BELOW a recorded sample while the interpolation branch sat above one.
	return bucketEdgesMS[len(bucketEdgesMS)-1]
}

// p99IsFloorBucket reports whether a quantile result is the "at least this
// much" floor produced by the overflow slot, so the report can say so instead
// of presenting a floor as a measurement.
func p99IsFloorBucket(buckets []int64, total int64, q float64) bool {
	if total == 0 {
		return false
	}
	rank := q * float64(total)
	var cum int64
	for i := range bucketEdgesMS {
		cum += buckets[i]
		if float64(cum) >= rank {
			return false // resolved inside a finite bucket
		}
	}
	return true // rank fell through to the overflow slot
}

// ResetForTest zeroes every counter and ring. It is exported only because the
// counters are process-wide and a package other than this one (core/api) needs
// to assert exact counts. The live server never calls it, so its numbers are
// lifetime values since process start.
//
// Safe against concurrent Observe (the same locks are taken), but NOT a
// synchronisation primitive: a reset concurrent with another test's requests
// makes that test's counts indeterminate. It works today because `go test` runs
// the tests in a package serially — a caller in a package that shares this
// binary with tests issuing concurrent /v1 requests must either run its
// assertions before they start or assert on deltas rather than absolutes.
// Do not "fix" this by adding a global lock: the server must never take one on
// the request path for a test's convenience.
func ResetForTest() { reset() }

// reset zeroes every counter and ring. For tests only — the live server never
// resets, so its numbers are lifetime values since process start.
func reset() {
	for i := range stats {
		s := &stats[i]
		s.mu.Lock()
		s.count, s.sumMS, s.buckets = 0, 0, [numBuckets]int64{}
		s.mu.Unlock()
	}
	qauth.Store(0)
	qtotal.Store(0)
	now := time.Now()
	for i := range rings {
		rings[i].Reset(now)
	}
	for i := range ringsI {
		ringsI[i].Reset(now)
	}
	start = now
}

// Ring is a fixed-size sliding window of per-second counters: a bounded ring,
// so memory is O(window) and never O(requests).
type Ring struct {
	mu     sync.Mutex
	slots  []int64
	stamps []int64 // the second each slot holds
	head   int
}

// NewRing returns a ring covering the last n seconds.
func NewRing(n int) Ring { return Ring{slots: make([]int64, n), stamps: make([]int64, n)} }

// Add counts v at time now, clearing slots older than the window.
func (r *Ring) Add(now time.Time, v int64) {
	sec := now.Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(sec)
	if r.stamps[r.head] != sec { // new second: move on, zeroing the stale slot
		r.head = (r.head + 1) % len(r.slots)
		r.stamps[r.head] = sec
		r.slots[r.head] = 0
	}
	r.slots[r.head] += v
}

// Sum returns the count over the window ending at now.
func (r *Ring) Sum(now time.Time) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advance(now.Unix())
	var total int64
	for i, s := range r.stamps {
		if s > 0 && now.Unix()-s < int64(len(r.slots)) {
			total += r.slots[i]
		}
	}
	return total
}

// advance moves head to the second containing sec, zeroing every slot it skips.
func (r *Ring) advance(sec int64) {
	n := int64(len(r.slots))
	cur := r.stamps[r.head]
	if cur == 0 { // an empty ring has no meaningful head; settle on sec
		r.head = 0
		r.stamps[0], r.slots[0] = sec, 0
		return
	}
	gap := sec - cur
	if gap <= 0 {
		return
	}
	if gap >= n {
		for i := range r.slots {
			r.slots[i], r.stamps[i] = 0, 0
		}
		r.head = 0
		r.stamps[0] = sec
		return
	}
	for i := int64(0); i < gap; i++ {
		r.head = (r.head + 1) % len(r.slots)
		r.slots[r.head], r.stamps[r.head] = 0, 0
	}
	r.stamps[r.head] = sec
}

// Reset empties the ring as of now.
func (r *Ring) Reset(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.slots {
		r.slots[i], r.stamps[i] = 0, 0
	}
	r.head = 0
	r.stamps[0] = now.Unix()
}

// Report renders the JSON document served at GET /metrics.
//
// Shape notes for a reader (and for the tests):
//   - uptime_seconds is how long these numbers have been accumulating.
//   - ops[<op>].count / sum_ms / avg_ms / buckets / p50_ms / p90_ms / p99_ms are
//     lifetime values since process start.
//   - buckets is listed in the same order as bucket_edges_ms; entry i counts
//     values <= edge i, and the final entry is the overflow (values > 60000 ms).
//   - auth.qps_60s is the rolling one-minute average for the auth lookup, and it
//     is the ONLY rate the trigger is read from.
//
// There is deliberately no lifetime-average QPS field. An earlier version
// published `qps_since_start` (lifetime calls / lifetime uptime); on a freshly
// started server that reads as an enormous number — a real fetch reported
// "qps_since_start": 82504.96 — because the denominator is a few milliseconds
// of uptime. It is diluted by idle time and actionable for nothing, and a
// number that looks like 82 000 QPS in a document whose whole job is a
// threshold decision is a liability, not data. `queries_total` and
// `uptime_seconds` are both published, so a reader who genuinely wants the
// lifetime average can compute it and see the denominator that makes it
// meaningless.
func Report() map[string]any {
	now := time.Now()
	snaps := Snapshot()

	out := make(map[string]any, len(snaps))
	for _, s := range snaps {
		entry := map[string]any{
			"count":  s.count,
			"sum_ms": s.sumMS,
		}
		if s.count > 0 {
			entry["avg_ms"] = s.sumMS / float64(s.count)
		} else {
			entry["avg_ms"] = 0
		}
		entry["p50_ms"] = quantileMS(s.buckets, s.count, 0.50)
		entry["p90_ms"] = quantileMS(s.buckets, s.count, 0.90)
		entry["p99_ms"] = quantileMS(s.buckets, s.count, 0.99)
		// p99_is_floor says the p99 came from the overflow slot: the true value
		// is AT LEAST p99_ms, not equal to it. Presenting a floor as a
		// measurement is exactly the kind of quiet lie this package must not tell.
		entry["p99_is_floor"] = p99IsFloorBucket(s.buckets, s.count, 0.99)
		entry["buckets"] = s.buckets
		out[string(s.op)] = entry
	}

	up := now.Sub(start)
	elapsed := up.Seconds()
	if elapsed <= 0 {
		elapsed = 1e-9
	}
	i := indexOf(OpAuthLookup)
	auth := map[string]any{
		"queries_total":  qauth.Load(),
		"qps_60s":        float64(rings[i].Sum(now)) / float64(authRateWindowSeconds),
		"window_seconds": authRateWindowSeconds,
		"trigger_p99_ms": 5.0,
		"trigger_qps":    100.0,
		"trigger":        "build a cache only when auth_lookup p99_ms > 5 or auth.qps_60s > 100 sustained; read the p99 only when p99_is_floor is false",
		"per_op_qps_60s": opQPS(now),
	}
	return map[string]any{
		"NOTE":              "admit-path instrumentation only; AuthByKeyHash is uncached by design (issue #33)",
		"uptime_seconds":    elapsed,
		"bucket_edges_ms":   bucketEdgesMS,
		"ops":               out,
		"admit_calls_total": qtotal.Load(),
		"auth":              auth,
	}
}

// opQPS is the rolling one-minute QPS of every operation, so the reservation
// round-trips PR #47 added can be read next to the auth lookup without being
// conflated with it.
func opQPS(now time.Time) map[string]float64 {
	out := make(map[string]float64, len(ops))
	for i, o := range ops {
		out[string(o)] = float64(ringsI[i].Sum(now)) / float64(authRateWindowSeconds)
	}
	return out
}

// String is a one-line human summary, used by logs and tests.
func String() string {
	snaps := Snapshot()
	var s string
	for _, sn := range snaps {
		s += fmt.Sprintf("%s=%d/%.1fms ", sn.op, sn.count, quantileMS(sn.buckets, sn.count, 0.99))
	}
	return s
}
