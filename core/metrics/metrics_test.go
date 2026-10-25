package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the admit-path instrumentation (#33).
//
// What these pin, and why each one exists:
//   - the counters record exactly the calls observed (a metrics package that
//     over- or under-counts makes the issue's trigger unreadable);
//   - bucket edges behave AT the edge, since the 5 ms trigger is compared with
//     the value on the edge — off-by-one there silently changes the verdict;
//   - the 60s ring expires old slots, so `qps_60s` cannot drift upward on a
//     server that has stopped taking traffic;
//   - no label can carry a user-supplied string, because Op is closed.
//
// reset() is called at the start of each test: these are package-level
// counters shared by the whole test binary.

// exact installs an Estimator that returns a fixed millisecond value, so a
// histogram can be driven without sleeping. It restores the real estimator
// afterwards, because another test (or the server under test) must not inherit
// synthetic timings.
func exact(t *testing.T, ms float64) {
	t.Helper()
	prev := Estimator
	Estimator = func(time.Duration) float64 { return ms }
	t.Cleanup(func() { Estimator = prev })
}

func report(t *testing.T) map[string]any {
	t.Helper()
	raw, err := json.Marshal(Report())
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return m
}

func opEntry(t *testing.T, m map[string]any, op string) map[string]any {
	t.Helper()
	ops, ok := m["ops"].(map[string]any)
	if !ok {
		t.Fatalf("report has no ops object: %v", m)
	}
	e, ok := ops[op].(map[string]any)
	if !ok {
		t.Fatalf("no entry for op %q in %v", op, ops)
	}
	return e
}

func num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("key %q missing or not numeric in %v", key, m)
	}
	return v
}

// TestObserve_CountsExactlyTheCallsMade: a known number of calls produces
// exactly that count, and only for the operation observed. This is the primary
// acceptance test — the whole issue depends on the counter being trustworthy.
func TestObserve_CountsExactlyTheCallsMade(t *testing.T) {
	reset()
	exact(t, 1.0)

	for i := 0; i < 7; i++ {
		Observe(OpAuthLookup, time.Millisecond)
	}
	for i := 0; i < 3; i++ {
		Observe(OpReserveAdd, time.Millisecond)
	}

	m := report(t)
	auth := opEntry(t, m, "auth_lookup")
	if got := num(t, auth, "count"); got != 7 {
		t.Errorf("auth_lookup count = %v, want 7", got)
	}
	if got := num(t, auth, "sum_ms"); got != 7 {
		t.Errorf("auth_lookup sum_ms = %v, want 7 (7 x 1ms)", got)
	}
	if got := num(t, auth, "avg_ms"); got != 1 {
		t.Errorf("auth_lookup avg_ms = %v, want 1", got)
	}
	reserve := opEntry(t, m, "reserve_add")
	if got := num(t, reserve, "count"); got != 3 {
		t.Errorf("reserve_add count = %v, want 3", got)
	}
	// The reservation round-trips must stay SEPARATE from the auth lookup, or
	// the issue's original trigger becomes unreadable.
	if got := num(t, reserve, "count"); got == num(t, auth, "count") {
		t.Error("reserve_add and auth_lookup share a counter")
	}
	// Untouched ops must stay at zero rather than inherit a neighbour's count.
	if got := num(t, opEntry(t, m, "reserve_settle"), "count"); got != 0 {
		t.Errorf("reserve_settle count = %v, want 0", got)
	}
	// admit_calls_total counts every op, so it is 10 here.
	if got := num(t, m, "admit_calls_total"); got != 10 {
		t.Errorf("admit_calls_total = %v, want 10", got)
	}
}

// TestBucketFor_EdgesExact: a value exactly ON an edge lands in that edge's own
// bucket (the standard <= edge contract), and the trigger comparison at 5 ms is
// therefore strict. Getting this wrong changes whether "p99 > 5 ms" fires.
//
// Expectations are DERIVED from bucketEdgesMS rather than hardcoded, so a
// deliberate layout change does not require rewriting this table — and a
// layout change that broke the <= edge contract still fails here.
func TestBucketFor_EdgesExact(t *testing.T) {
	// Every edge, exactly on it.
	for i, edge := range bucketEdgesMS {
		if got := bucketFor(edge); got != i {
			t.Errorf("bucketFor(%v) = %d, want %d (exactly on edge %d)", edge, got, i, i)
		}
		// Just past it belongs to the next bucket.
		just := edge * 1.0000001
		if just <= edge {
			just = edge + 1e-6
		}
		if got := bucketFor(just); got != i+1 {
			t.Errorf("bucketFor(%v) = %d, want %d (just past edge %d)", just, got, i+1, i)
		}
	}
	// Below the first edge, and in the overflow slot.
	if got := bucketFor(0); got != 0 {
		t.Errorf("bucketFor(0) = %d, want 0", got)
	}
	last := len(bucketEdgesMS)
	if got := bucketFor(bucketEdgesMS[last-1] * 2); got != last {
		t.Errorf("bucketFor(2x last edge) = %d, want %d (overflow)", got, last)
	}
	if got := bucketFor(1e9); got != last {
		t.Errorf("bucketFor(1e9) = %d, want %d (overflow, no panic)", got, last)
	}
	// The contract the trigger depends on: 5 ms exactly is NOT past the 5 ms
	// edge, and the p99 interpolation must therefore be able to report <= 5.
	five := slices.Index(bucketEdgesMS, 5)
	if five < 0 {
		t.Fatal("the 5 ms edge is gone; the trigger cannot be evaluated")
	}
	if got := bucketFor(5); got != five {
		t.Errorf("bucketFor(5) = %d, want %d (the trigger threshold sits on an edge)", got, five)
	}

	// Negative durations are clamped by Observe, not by bucketFor; assert the
	// clamp end to end because a negative would otherwise land in bucket 0 and
	// understate the p99.
	reset()
	exact(t, -5)
	Observe(OpAuthLookup, time.Millisecond)
	if got := num(t, opEntry(t, report(t), "auth_lookup"), "sum_ms"); got != 0 {
		t.Errorf("negative duration contributed %v ms to sum, want 0", got)
	}
}

// TestObserve_NonFiniteIsDropped: a NaN or Inf duration must be dropped, not
// recorded. A NaN reaching sumMS would poison sum_ms/avg_ms for the process
// lifetime and then fail JSON encoding inside the /metrics handler AFTER the
// 200 status line was written, truncating the body.
func TestObserve_NonFiniteIsDropped(t *testing.T) {
	reset()
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		exact(t, bad)
		Observe(OpAuthLookup, time.Millisecond)
	}
	// Then one good sample, so we can tell "dropped" from "nothing recorded".
	exact(t, 3)
	Observe(OpAuthLookup, time.Millisecond)

	auth := opEntry(t, report(t), "auth_lookup")
	if got := num(t, auth, "count"); got != 1 {
		t.Errorf("count = %v, want 1 (the three non-finite observations must be dropped)", got)
	}
	if got := num(t, auth, "sum_ms"); got != 3 {
		t.Errorf("sum_ms = %v, want 3 (a non-finite value must not poison the sum)", got)
	}
	if got := num(t, auth, "avg_ms"); got != 3 {
		t.Errorf("avg_ms = %v, want 3", got)
	}
	// And the whole document must still be JSON-encodable.
	if _, err := json.Marshal(Report()); err != nil {
		t.Fatalf("Report() is not JSON-encodable after a non-finite observation: %v", err)
	}
}

// TestBuckets_CountInTheRightSlot drives one value per slot and checks the
// histogram array itself, not just the derived percentiles. Expected slots are
// derived from the layout so this test tracks a deliberate change and still
// catches a value landing in the wrong place.
func TestBuckets_CountInTheRightSlot(t *testing.T) {
	reset()
	// One value per slot, overflow included, so every slot is exercised. Slots are
	// computed from the layout: the midpoint of each finite bucket, then a value
	// past the last edge for the overflow slot. Deriving them means a deliberate
	// layout change needs no edit here, while a value landing in the wrong slot
	// still fails.
	type sample struct {
		ms   float64
		slot int
	}
	var values []sample
	for i, edge := range bucketEdgesMS {
		lo := 0.0
		if i > 0 {
			lo = bucketEdgesMS[i-1]
		}
		mid := (lo + edge) / 2
		if mid <= lo {
			// Buckets this narrow (0.1 ms at the bottom) get their edge.
			mid = edge
		}
		values = append(values, sample{mid, i})
	}
	values = append(values, sample{bucketEdgesMS[len(bucketEdgesMS)-1] * 1.5, len(bucketEdgesMS)})
	for _, v := range values {
		exact(t, v.ms)
		Observe(OpAuthLookup, time.Millisecond)
	}
	buckets, ok := opEntry(t, report(t), "auth_lookup")["buckets"].([]any)
	if !ok {
		t.Fatalf("buckets missing from report")
	}
	if len(buckets) != len(bucketEdgesMS)+1 {
		t.Fatalf("got %d buckets, want %d (edges + overflow)", len(buckets), len(bucketEdgesMS)+1)
	}
	for i, b := range buckets {
		want := 0.0
		for _, v := range values {
			if v.slot == i {
				want = 1
			}
		}
		if b.(float64) != want {
			t.Errorf("bucket[%d] (%s] = %v, want %v", i, edgeAt(i), b, want)
		}
	}
	// A pathological value must be counted in the overflow slot, not dropped.
	if got := buckets[len(buckets)-1].(float64); got != 1 {
		t.Errorf("overflow slot = %v, want 1 (a 90s sample must not be dropped)", got)
	}
	// And the buckets must be a cumulative-compatible layout: monotone edges.
	for i := 1; i < len(bucketEdgesMS); i++ {
		if bucketEdgesMS[i] <= bucketEdgesMS[i-1] {
			t.Fatalf("bucket edges not strictly increasing at %d: %v", i, bucketEdgesMS)
		}
	}
}

// edgeAt names the upper edge of bucket i for error messages.
func edgeAt(i int) string {
	if i >= len(bucketEdgesMS) {
		return "+Inf"
	}
	return strings.TrimRight(strings.TrimRight(
		time.Duration(bucketEdgesMS[i]*float64(time.Millisecond)).String(), "0"), ".") + "ms"
}

// TestQuantile_InterpolatesWithinBucket: percentiles are read off the buckets
// with interpolation inside the bucket holding the target rank. This is the
// number the "p99 > 5 ms" trigger is compared against, so both the fast case
// (does not fire) and the slow case (does fire) are pinned.
func TestQuantile_InterpolatesWithinBucket(t *testing.T) {
	reset()
	// 95 samples at 1 ms, 5 at 8 ms. The 99th percentile is a slow sample, so
	// the trigger must fire. (Note that ONE slow sample in 100 is NOT enough:
	// the true 99th value there is the fast one. The threshold is a
	// distribution statement, not a single-outlier alarm.)
	for i := 0; i < 95; i++ {
		exact(t, 1)
		Observe(OpAuthLookup, time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		exact(t, 8)
		Observe(OpAuthLookup, time.Millisecond)
	}

	auth := opEntry(t, report(t), "auth_lookup")
	p50, p90, p99 := num(t, auth, "p50_ms"), num(t, auth, "p90_ms"), num(t, auth, "p99_ms")
	if p50 < 0.5 || p50 > 1 {
		t.Errorf("p50_ms = %v, want ~1 (inside the (0.5,1] bucket)", p50)
	}
	if p99 <= 5 {
		t.Errorf("p99_ms = %v, want > 5 (5%% of samples at 8ms must fire the trigger)", p99)
	}
	if p99 > 10 {
		t.Errorf("p99_ms = %v, want <= 10 (interpolated inside the (5,10] bucket)", p99)
	}
	if p90 > p99 {
		t.Errorf("p90 %v > p99 %v", p90, p99)
	}

	// The fast case: everything at 1 ms must NOT fire the 5 ms trigger, or the
	// metric would be useless as a gate.
	reset()
	for i := 0; i < 100; i++ {
		exact(t, 1)
		Observe(OpAuthLookup, time.Millisecond)
	}
	fast := opEntry(t, report(t), "auth_lookup")
	for _, k := range []string{"p50_ms", "p90_ms", "p99_ms"} {
		if got := num(t, fast, k); got > 5 {
			t.Errorf("all-1ms distribution %s = %v, want <= 5 (must not fire)", k, got)
		}
	}
}

// TestReport_EmptyIsZeroNotNaN: a fresh process must serve zeros, not NaN or a
// missing key, so a dashboard can read it before any traffic arrives.
func TestReport_EmptyIsZeroNotNaN(t *testing.T) {
	reset()
	m := report(t)
	auth := opEntry(t, m, "auth_lookup")
	for _, k := range []string{"count", "sum_ms", "avg_ms", "p50_ms", "p90_ms", "p99_ms"} {
		if got := num(t, auth, k); got != 0 {
			t.Errorf("fresh auth_lookup %s = %v, want 0", k, got)
		}
	}
	a := m["auth"].(map[string]any)
	if got := num(t, a, "queries_total"); got != 0 {
		t.Errorf("queries_total = %v, want 0", got)
	}
	if got := num(t, a, "qps_60s"); got != 0 {
		t.Errorf("qps_60s = %v, want 0", got)
	}
	// The thresholds are part of the payload: a reader must not have to go
	// looking for them in the source to evaluate the trigger.
	if got := num(t, a, "trigger_p99_ms"); got != 5 {
		t.Errorf("trigger_p99_ms = %v, want 5", got)
	}
	if got := num(t, a, "trigger_qps"); got != 100 {
		t.Errorf("trigger_qps = %v, want 100", got)
	}
	if s, _ := a["trigger"].(string); !strings.Contains(s, "auth_lookup") {
		t.Errorf("trigger text does not name the metric: %q", s)
	}
	// And the same two thresholds must be stated where a source reader looks: in
	// the PACKAGE DOC BLOCK, which is the first thing godoc shows. Scoped to
	// that block rather than the whole file, so an unrelated comment cannot
	// satisfy it.
	src := packageDoc(t)
	for _, want := range []string{"5 ms", "100 queries/second"} {
		if !strings.Contains(src, want) {
			t.Errorf("package doc block does not state the threshold %q", want)
		}
	}
	// The doc must also not repeat the two claims the review falsified: the
	// bogus 2 ms error bound, and "lower bound" as an unconditional promise.
	if strings.Contains(src, "30->50") {
		t.Error("package doc still cites the nonexistent 30->50 ms bucket")
	}
	if strings.Contains(src, "worst-case error is 2 ms") {
		t.Error("package doc still claims a 2 ms worst-case error bound")
	}
}

// TestRing_ExpiresAndRolls: the 60s window must forget. A ring that only
// accumulates would report a sustained QPS forever after a burst ended, which
// would fire the trigger on a quiet server.
func TestRing_ExpiresAndRolls(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	r := NewRing(60)
	r.Add(now, 5)
	if got := r.Sum(now); got != 5 {
		t.Fatalf("Sum at t = %d, want 5", got)
	}
	// 59s later the slot is still inside the window.
	later := now.Add(59 * time.Second)
	if got := r.Sum(later); got != 5 {
		t.Errorf("Sum at t+59s = %d, want 5 (still inside the window)", got)
	}
	// 60s later it has aged out.
	if got := r.Sum(now.Add(60 * time.Second)); got != 0 {
		t.Errorf("Sum at t+60s = %d, want 0 (aged out)", got)
	}
	// Same second accumulates rather than overwriting.
	r2 := NewRing(60)
	r2.Add(now, 1)
	r2.Add(now, 2)
	if got := r2.Sum(now); got != 3 {
		t.Errorf("two Adds in one second = %d, want 3", got)
	}
	// A long gap must not read stale slots, and must not overflow the ring.
	r3 := NewRing(3)
	r3.Add(now, 9)
	if got := r3.Sum(now.Add(10 * time.Second)); got != 0 {
		t.Errorf("after a 10s gap over a 3s ring, Sum = %d, want 0", got)
	}
	r3.Add(now.Add(10*time.Second), 4)
	if got := r3.Sum(now.Add(10 * time.Second)); got != 4 {
		t.Errorf("Sum after rotation = %d, want 4", got)
	}
}

// TestObserve_QPSWindowReflectsRecentTraffic: qps_60s is the number the
// ">100 queries/second sustained" trigger is read from, so it must be the
// recent rate and not a lifetime average that never recovers.
func TestObserve_QPSWindowReflectsRecentTraffic(t *testing.T) {
	reset()
	for i := 0; i < 120; i++ {
		Observe(OpAuthLookup, time.Millisecond)
	}
	a := report(t)["auth"].(map[string]any)
	// 120 calls in a fraction of a second, averaged over the 60s window.
	qps := num(t, a, "qps_60s")
	if qps <= 0 || qps > 120 {
		t.Errorf("qps_60s = %v, want a small positive rate for 120 calls in one second", qps)
	}
	if got := num(t, a, "queries_total"); got != 120 {
		t.Errorf("queries_total = %v, want 120", got)
	}
	// auth_lookup's own rate must be reported alongside the others without
	// being merged into them.
	perOp, ok := a["per_op_qps_60s"].(map[string]any)
	if !ok {
		t.Fatalf("per_op_qps_60s missing: %v", a)
	}
	if num(t, perOp, "auth_lookup") <= 0 {
		t.Error("per_op_qps_60s[auth_lookup] is not positive after 120 lookups")
	}
	if num(t, perOp, "reserve_add") != 0 {
		t.Error("per_op_qps_60s[reserve_add] should be 0; the ops are being conflated")
	}
}

// TestObserve_ConcurrentIsSafeAndExact: the auth middleware fires from every
// request goroutine, so Observe must be race-free and must not lose counts
// under contention. Run with -race to make the first half meaningful.
func TestObserve_ConcurrentIsSafeAndExact(t *testing.T) {
	reset()
	const goroutines, each = 8, 250
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				Observe(OpAuthLookup, time.Millisecond)
				Observe(OpReserveAdd, 2*time.Millisecond)
			}
		}()
	}
	wg.Wait()
	m := report(t)
	if got := num(t, opEntry(t, m, "auth_lookup"), "count"); got != goroutines*each {
		t.Errorf("auth_lookup count = %v, want %d", got, goroutines*each)
	}
	if got := num(t, opEntry(t, m, "reserve_add"), "count"); got != goroutines*each {
		t.Errorf("reserve_add count = %v, want %d", got, goroutines*each)
	}
}

// TestOps_ClosedAndBounded: the label set is a compile-time enumeration, which
// is what makes it impossible for a raw key or token to become a metric label.
// If someone ever swaps Op for a free string, this fails.
func TestOps_ClosedAndBounded(t *testing.T) {
	if len(ops) > 32 {
		t.Errorf("op set grew to %d; a bounded label set was the point", len(ops))
	}
	seen := map[Op]bool{}
	for _, o := range ops {
		if seen[o] {
			t.Errorf("duplicate op %q", o)
		}
		seen[o] = true
		if !strings.HasPrefix(string(o), "auth_") && !strings.HasPrefix(string(o), "reserve_") &&
			o != OpTPMRead && o != OpRPMHit && o != OpReservedRead {
			t.Errorf("op %q does not look like a fixed admit-path operation", o)
		}
	}
	if indexOf(OpAuthLookup) == indexOf(OpReserveAdd) {
		t.Error("auth_lookup and reserve_add share a slot")
	}
	// An unknown op must not panic a request path.
	Observe(Op("not_a_real_op"), time.Millisecond)
}

// packageDoc returns ONLY this package's leading doc comment (the block above
// `package metrics`), located relative to THIS FILE rather than relative to the
// working directory.
//
// The earlier version did os.ReadFile("metrics.go") and returned the whole
// file: that relied on `go test` happening to set CWD to the package directory,
// and asserted Contains over comments and code alike, so the acceptance
// criterion could be satisfied by an unrelated comment or a stray string
// literal anywhere in metrics.go. Scoping the read to the doc block means the
// assertion is about the documentation a reader actually sees first.
func packageDoc(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate the test file")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "metrics.go"))
	if err != nil {
		t.Fatalf("read metrics.go beside %s: %v", thisFile, err)
	}
	text := string(src)
	end := strings.Index(text, "\npackage metrics\n")
	if end < 0 {
		t.Fatal("could not find the package clause in metrics.go")
	}
	return text[:end]
}

// TestBucketLayout_ErrorBoundMatchesDoc pins the documented worst-case error
// bound against the REAL layout.
//
// This test has now defended a wrong number twice, which is why it is written
// the way it is. Round 1: the doc claimed a 2 ms bound and named a bucket that
// did not exist. Round 2: the doc (and this test, which asserted the literal
// "55 000") said the top bucket was 55 000 ms wide when 60000-30000 is 30 000 —
// the old 5000->60000 width, carried over wrongly. A test that hardcodes the
// expected number turns a doc typo into an enforced invariant.
//
// So the expected value is DERIVED here: the widest finite bucket is computed
// from bucketEdgesMS, and the doc is required to state that computed number.
// The test can no longer pin a stale literal — it fails when the doc is wrong,
// when a bucket is widened so the doc goes stale, and when the two drift apart
// in either direction.
func TestBucketLayout_ErrorBoundMatchesDoc(t *testing.T) {
	if len(bucketEdgesMS)+1 != numBuckets {
		t.Fatalf("numBuckets = %d but len(edges)+1 = %d; the array length constant drifted",
			numBuckets, len(bucketEdgesMS)+1)
	}

	// Derive the widest finite bucket (and which pair it is) from the layout.
	widest, lo, hi := 0.0, 0.0, 0.0
	prev := 0.0
	for _, e := range bucketEdgesMS {
		if e-prev > widest {
			widest, lo, hi = e-prev, prev, e
		}
		prev = e
	}
	if widest <= 0 {
		t.Fatal("no finite bucket has positive width; the layout is degenerate")
	}

	doc := packageDoc(t)

	// 1. The doc must name the widest bucket by its edges, using the SAME
	//    rendering this test now computes — no independent literal.
	pairText := fmt.Sprintf("%s->%s", plainMS(lo), plainMS(hi))
	if !strings.Contains(doc, pairText) {
		t.Errorf("package doc does not name the widest bucket %s (computed from bucketEdgesMS)", pairText)
	}

	// 2. The doc must state the WIDTH, in the sentence where it states the bound:
	//    "<n> ms wide". Matching the number ALONE is too weak — a bare "30000"
	//    also appears in the edge pair text and in bucketEdgesMS's own comment,
	//    so a wrong width elsewhere still contains the right digits somewhere
	//    and the assertion passes (verified by mutation: changing the width to
	//    12 345 left a number-only check green). Requiring the sentential form
	//    is what makes a wrong width actually fail.
	claimSpaced := spacedMS(widest) + " ms wide"
	claimPlain := plainMS(widest) + " ms wide"
	if !strings.Contains(doc, claimSpaced) && !strings.Contains(doc, claimPlain) {
		t.Errorf("package doc does not state the computed worst-case width in the form %q or %q;\n"+
			"the widest finite bucket is %s at %v ms",
			claimSpaced, claimPlain, pairText, widest)
	}
	// 3. And no OTHER "N ms wide" claim may appear: a doc stating two different
	//    widths is worse than one stating none. This closes the hole mutation B
	//    found — a wrong width is caught even when the right digits are present
	//    elsewhere in the block.
	for _, m := range widthClaim.FindAllStringSubmatch(doc, -1) {
		got := strings.NewReplacer(" ", "", "\u00a0", "").Replace(m[1])
		if got != plainMS(widest) {
			t.Errorf("package doc claims a worst-case width of %q, but the widest finite bucket is %v ms (the %s bucket)",
				m[0], widest, pairText)
		}
	}
	// 4. The historical wrong values must not reappear at all.
	for _, stale := range []string{"55 000", "55000", "2 ms (the"} {
		if strings.Contains(doc, stale) {
			t.Errorf("package doc still contains the stale bound %q", stale)
		}
	}

	// 4. The buckets around the trigger must be narrow, since that is where the
	//    decision is made — the claim that makes a wide top acceptable.
	for _, want := range []float64{5, 10} {
		if slices.Index(bucketEdgesMS, want) < 0 {
			t.Fatalf("edge %v ms missing; the trigger is not decidable", want)
		}
	}
	loI, hiI := slices.Index(bucketEdgesMS, 2), slices.Index(bucketEdgesMS, 10)
	if loI < 0 || hiI < 0 || bucketEdgesMS[hiI]-bucketEdgesMS[loI] > 10 {
		t.Error("the buckets spanning the 5 ms trigger are wider than 10 ms; resolution around the trigger is too coarse")
	}
	// 5. The doc's trigger-resolution claim must match the real (5,10] width:
	//    the doc now says "within 5 ms", so assert the bucket really is 5 ms.
	lo5, hi5 := slices.Index(bucketEdgesMS, 5), slices.Index(bucketEdgesMS, 10)
	if lo5 < 0 || hi5 < 0 {
		t.Fatal("the 5 ms or 10 ms edge is missing")
	}
	if w := bucketEdgesMS[hi5] - bucketEdgesMS[lo5]; w != 5 {
		t.Errorf("the (5,10] bucket is %v ms wide but the doc claims resolution \"within 5 ms\"; update the doc", w)
	}
}

// widthClaim matches the doc's sentential width claims, e.g. "30 000 ms wide",
// capturing the number. Used to catch a doc that states a width other than the
// computed one, even when the correct digits appear somewhere else.
var widthClaim = regexp.MustCompile(`([0-9][0-9  ]*) ms wide`)

// plainMS renders a millisecond count without separators ("30000"), matching
// how the package doc writes bucket EDGES (30000->60000).
func plainMS(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// spacedMS renders a millisecond count with a thousands separator ("30 000"),
// matching how the package doc writes the WIDTH ("30 000 ms wide").
func spacedMS(v float64) string {
	s := plainMS(v)
	if len(s) <= 4 { // 60000 is written "60000" in the edges; only widths get spaced
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

// TestQuantile_NeverReportsImpossibleValue pins the invariant the review
// demanded: a p99 must never be reported above a value the histogram actually
// recorded, nor above the highest bucket edge.
//
// The bug: the overflow branch returned a flat 60000 while the in-bucket branch
// returned the bucket's `hi`, so the same distribution could be reported above
// one recorded sample and below another. Measured on the old code: 100 samples
// at 90 s produced p99 = 60000 (true 90000), and the 95x1ms+5x8ms fixture
// produced 9.0 against a true 8.0.
func TestQuantile_NeverReportsImpossibleValue(t *testing.T) {
	// Case A: the reviewer's overflow repro. Every sample is 90 s, well past the
	// last edge. The floor is the last edge, and it must be flagged as a floor.
	reset()
	exact(t, 90000)
	for i := 0; i < 100; i++ {
		Observe(OpAuthLookup, time.Millisecond)
	}
	auth := opEntry(t, report(t), "auth_lookup")
	p99 := num(t, auth, "p99_ms")
	if p99 > bucketEdgesMS[len(bucketEdgesMS)-1] {
		t.Errorf("p99 = %v exceeds the highest edge %v; an impossible value was reported",
			p99, bucketEdgesMS[len(bucketEdgesMS)-1])
	}
	if p99 != bucketEdgesMS[len(bucketEdgesMS)-1] {
		t.Errorf("p99 = %v, want the last edge %v as a floor", p99, bucketEdgesMS[len(bucketEdgesMS)-1])
	}
	if isFloor, ok := auth["p99_is_floor"].(bool); !ok || !isFloor {
		t.Errorf("p99_is_floor = %v, want true: this p99 is a floor, not a measurement", auth["p99_is_floor"])
	}

	// Case B: the reviewer's fixture. 95 samples at 1 ms and 5 at 8 ms. The p99
	// resolves inside the (5,10] bucket, and interpolation assumes those 5
	// samples are spread across the bucket, so it reports 9.0 against a true 8.0.
	//
	// That is NOT an impossible value, so the assertion below is deliberately
	// not "p99 <= 8": 9.0 lies inside the bucket that actually holds the sample.
	// It is bucket-resolution error, inherent to a histogram and documented in
	// the package comment. What must hold — and what the old code violated — is
	// that the value never escapes the bucket it came from and never exceeds the
	// highest edge.
	reset()
	for i := 0; i < 95; i++ {
		exact(t, 1)
		Observe(OpAuthLookup, time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		exact(t, 8)
		Observe(OpAuthLookup, time.Millisecond)
	}
	auth = opEntry(t, report(t), "auth_lookup")
	p99 = num(t, auth, "p99_ms")
	if p99 <= 5 {
		t.Errorf("p99 = %v, want > 5 (5%% of samples at 8 ms must still fire the trigger)", p99)
	}
	if p99 > 10 {
		t.Errorf("p99 = %v escaped the (5,10] bucket that holds the sample", p99)
	}
	if isFloor, _ := auth["p99_is_floor"].(bool); isFloor {
		t.Error("p99_is_floor = true, but this p99 resolved inside a finite bucket")
	}

	// Case C: monotonicity across quantiles over one mixed distribution, and
	// every quantile within [0, lastEdge].
	reset()
	for _, ms := range []float64{0.05, 0.3, 1.2, 4, 7, 30, 300, 9000} {
		exact(t, ms)
		for i := 0; i < 10; i++ {
			Observe(OpAuthLookup, time.Millisecond)
		}
	}
	auth = opEntry(t, report(t), "auth_lookup")
	p50, p90, p99 := num(t, auth, "p50_ms"), num(t, auth, "p90_ms"), num(t, auth, "p99_ms")
	if !(p50 <= p90 && p90 <= p99) {
		t.Errorf("quantiles not monotone: p50=%v p90=%v p99=%v", p50, p90, p99)
	}
	last := bucketEdgesMS[len(bucketEdgesMS)-1]
	for name, v := range map[string]float64{"p50": p50, "p90": p90, "p99": p99} {
		if v < 0 || v > last {
			t.Errorf("%s = %v is outside [0, %v]", name, v, last)
		}
	}
}

// TestQuantile_EmptyAndSingle: degenerate inputs must not panic or divide by
// zero — an empty histogram is the state of every fresh process.
func TestQuantile_EmptyAndSingle(t *testing.T) {
	var zero [numBuckets]int64
	if got := quantileMS(zero[:], 0, 0.99); got != 0 {
		t.Errorf("empty histogram p99 = %v, want 0", got)
	}
	if p99IsFloorBucket(zero[:], 0, 0.99) {
		t.Error("empty histogram reported as a floor bucket")
	}
	// One sample, plus a rank that lands exactly on a cumulative boundary.
	var one [numBuckets]int64
	one[bucketFor(3)] = 1
	for _, q := range []float64{0.01, 0.5, 0.99, 1.0} {
		got := quantileMS(one[:], 1, q)
		if got < 2 || got > 5 {
			t.Errorf("single 3ms sample at q=%v = %v, want within its (2,5] bucket", q, got)
		}
	}
}
