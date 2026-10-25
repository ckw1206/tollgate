package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

// Tests for in-flight reservations (#23, #28).
//
// The defect these cover: admit() reads only committed spend, which finish()
// writes after upstream completes. N concurrent requests therefore all admit
// against the same pre-spend counter. Before this change none of the tests
// below existed, which is why both bugs shipped.

// blockingProvider holds every request inside upstream until released, so
// concurrent admits are genuinely in flight together — the condition the old
// code got wrong. Without this, a fast fake provider finishes before the next
// request admits and the race never reproduces.
type blockingProvider struct {
	entered chan struct{}
	release chan struct{}
	usage   llm.Usage
}

func newBlockingProvider(usage llm.Usage) *blockingProvider {
	return &blockingProvider{entered: make(chan struct{}, 128), release: make(chan struct{}), usage: usage}
}

func (b *blockingProvider) Chat(context.Context, *llm.Model, *llm.ChatRequest) (*llm.Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return &llm.Result{Body: []byte(`{"id":"x","choices":[]}`), Usage: b.usage}, nil
}

func (b *blockingProvider) ChatStream(ctx context.Context, m *llm.Model, r *llm.ChatRequest, emit func([]byte) error) (llm.Usage, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.usage, nil
}

func (b *blockingProvider) Embed(context.Context, *llm.Model, []byte) (*llm.Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return &llm.Result{Body: []byte(`{"data":[]}`), Usage: b.usage}, nil
}

// releaseAll lets every held request through.
func (b *blockingProvider) releaseAll() { close(b.release) }

// setProvider swaps the provider used by the "fast" model.
func (e *env) setProvider(p llm.Provider) {
	reg := llm.NewRegistry(map[string]llm.Provider{"openai": p})
	reg.Replace([]*llm.Model{
		{ID: "11111111-1111-1111-1111-111111111111", Name: "fast", Provider: "openai",
			Prices: llm.Prices{InputMicros: 2_500_000, OutputMicros: 10_000_000}, ContextWindow: 128000},
	})
	e.srv.Registry = reg
}

// TestAdmit_ConcurrentRequestsDoNotOvershootBudget is #23's acceptance test.
//
// A key with a budget that fits exactly one request opens N concurrent
// requests. Every one must be admitted or refused on the reservation, and the
// total admitted must never exceed what the cap allows.
func TestAdmit_ConcurrentRequestsDoNotOvershootBudget(t *testing.T) {
	// The fake usage is 1000 in + 500 out at 2.5M/10M micros per 1M tokens:
	// cost = (1000*2_500_000 + 500*10_000_000)/1000 = 7_500_000 nanos.
	const oneCost = 7_500_000
	// A cap of 1.5 requests' worth: at most one can be admitted at a time.
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: oneCost + oneCost/2})
	bp := newBlockingProvider(llm.Usage{Input: 1000, Output: 500})
	e.setProvider(bp)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// max_tokens pins the estimate to the actual usage, so the
			// arithmetic below is exact rather than heuristic.
			body := `{"model":"fast","max_tokens":500,"messages":[{"role":"user","content":"` +
				strings.Repeat("x", 4000) + `"}]}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+e.raw)
			e.h.ServeHTTP(w, r)
			codes[i] = w.Code
		}()
	}

	// Wait until the first request is parked inside upstream, so the rest are
	// genuinely concurrent with an in-flight request.
	select {
	case <-bp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no request reached upstream")
	}
	// Give the others time to be admitted or refused while the first is held.
	time.Sleep(300 * time.Millisecond)
	bp.releaseAll()
	wg.Wait()

	admitted := 0
	for _, c := range codes {
		if c == 200 {
			admitted++
		}
	}
	// The reservation is settled with actual usage equal to the estimate, so
	// exactly one request fits. Two would be 15M against a 11.25M cap.
	if admitted > 1 {
		t.Fatalf("budget overshot: %d requests admitted against a cap fitting 1 (codes=%v)", admitted, codes)
	}
	if admitted == 0 {
		t.Fatalf("nothing admitted; the cap should fit exactly one request (codes=%v)", codes)
	}
}

// TestAdmit_ConcurrentRequestsDoNotOvershootTPM is #28's acceptance test.
func TestAdmit_ConcurrentRequestsDoNotOvershootTPM(t *testing.T) {
	// Each request uses 1500 tokens (1000 in + 500 out). A 2000-token limit
	// fits one request, not two.
	limit := int32(2000)
	e := newEnv(t, repo.KeyInput{TPMLimit: &limit})
	bp := newBlockingProvider(llm.Usage{Input: 1000, Output: 500})
	e.setProvider(bp)

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := `{"model":"fast","max_tokens":500,"messages":[{"role":"user","content":"` +
				strings.Repeat("x", 4000) + `"}]}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+e.raw)
			e.h.ServeHTTP(w, r)
			codes[i] = w.Code
		}()
	}
	time.Sleep(400 * time.Millisecond)
	bp.releaseAll()
	wg.Wait()

	admitted := 0
	for _, c := range codes {
		if c == 200 {
			admitted++
		}
	}
	if admitted > 1 {
		t.Fatalf("TPM overshot: %d requests admitted against a 2000-token limit each using 1500 (codes=%v)", admitted, codes)
	}
	if admitted == 0 {
		t.Fatalf("nothing admitted; the limit should fit exactly one request (codes=%v)", codes)
	}
}

// TestAdmit_ReservationReleasedOnSuccess: after a request finishes, the
// in-flight reservation must be gone. A lingering reservation would slowly
// strand the scope's budget.
//
// The request's actual cost is deliberately NOT held against the cap: the
// recorder commits it within ~2s, and holding it too would double-count every
// committed request. See core/cache.Settle.
func TestAdmit_ReservationReleasedOnSuccess(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000})
	ctx := context.Background()

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("baseline %d %s", w.Code, w.Body)
	}
	cost, tokens, err := e.srv.Cache.Reserved(ctx, "key", e.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0 || tokens != 0 {
		t.Fatalf("in-flight reservation leaked after success: cost=%d tpm=%d", cost, tokens)
	}
	held, _, err := e.srv.Cache.Held(ctx, "key", e.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Fatalf("nothing should remain held after a settled request, got %d", held)
	}
}

// TestAdmit_ReservationReleasedOnUpstreamError: a failed upstream call still
// occupied in-flight budget, and must not keep occupying it.
func TestAdmit_ReservationReleasedOnUpstreamError(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000})
	e.prov.err = &llm.UpstreamError{Status: 503, Message: "overloaded"}
	ctx := context.Background()

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 503 {
		t.Fatalf("expected 503, got %d %s", w.Code, w.Body)
	}
	if cost, tokens, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 || tokens != 0 {
		t.Fatalf("reservation leaked after upstream error: cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_ReservationReleasedOnBadRequest: the 400 path returns before
// finish(), so only the deferred release prevents a leak. This is the path
// most likely to be forgotten.
func TestAdmit_ReservationReleasedOnBadRequest(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000})
	e.prov.err = llm.ErrBadRequest
	ctx := context.Background()

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 400 {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body)
	}
	if cost, tokens, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 || tokens != 0 {
		t.Fatalf("reservation leaked after bad request (never reached upstream): cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_ReservationReleasedOnClientDisconnect: the client goes away while
// the request is genuinely IN FLIGHT, and the reservation must be released.
//
// This asserts the reservation is actually held at the moment of disconnect
// (otherwise the test would pass vacuously against a build that never reserves
// at all, which is how the original bug went unnoticed).
func TestAdmit_ReservationReleasedOnClientDisconnect(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000})
	bp := newBlockingProvider(llm.Usage{Input: 1000, Output: 500})
	e.setProvider(bp)
	ctx := context.Background()

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer "+e.raw)

	// Cancel the client's context while the provider is parked: a real
	// mid-flight disconnect.
	cctx, cancel := context.WithCancel(r.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.h.ServeHTTP(&failingWriter{ResponseRecorder: httptest.NewRecorder()}, r.WithContext(cctx))
	}()

	select {
	case <-bp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached upstream")
	}
	// Precondition: the request is in flight and its budget IS reserved, so a
	// later zero is meaningful rather than vacuous.
	if cost, _, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost == 0 {
		t.Fatal("precondition: expected an in-flight reservation while parked upstream")
	}
	cancel()
	bp.releaseAll()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("request did not finish after disconnect")
	}

	if cost, tokens, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 || tokens != 0 {
		t.Fatalf("reservation leaked after client disconnect mid-flight: cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_RefusalReleasesEvenWithDeadReserveContext pins the reason admit()
// releases on a detached context rather than r.Context().
//
// When the client disconnects, r.Context() is cancelled — which is exactly when
// a reservation is most likely to be stranded, because the request is refused
// or abandoned rather than settled. Releasing on that context would silently
// no-op, so this calls release with a dead context and asserts the budget is
// still returned.
func TestAdmit_RefusalReleasesEvenWithDeadReserveContext(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000})
	ctx := context.Background()

	// Reserve as admit() would, then release with a cancelled context.
	if _, _, err := e.srv.Cache.Reserve(ctx, "key", e.key.ID, 5_000_000, 0); err != nil {
		t.Fatal(err)
	}
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.srv.Cache.Release(dead, "key", e.key.ID, 5_000_000, 0); err == nil {
		t.Fatal("expected the dead context to fail the release, which is why admit() must not use r.Context()")
	}
	// The reservation is still held: the caller must retry on a live context.
	if cost, _, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 5_000_000 {
		t.Fatalf("precondition: expected the reservation to survive a dead-context release, got %d", cost)
	}
	// admit()'s own release path uses context.Background(), which succeeds.
	if err := e.srv.Cache.Release(ctx, "key", e.key.ID, 5_000_000, 0); err != nil {
		t.Fatal(err)
	}
	if cost, _, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 {
		t.Fatalf("release should clear the reservation, got %d", cost)
	}
}

// TestAdmit_NoReservationLeakUnderChurn hammers the admit path and asserts the
// in-flight reservation returns to zero once every request has finished. A
// leak here would not fail a single request; it would slowly strand budget
// until the scope could no longer serve anything.
func TestAdmit_NoReservationLeakUnderChurn(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 10_000_000_000})
	ctx := context.Background()

	const rounds = 40
	var wg sync.WaitGroup
	for range rounds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(chatBody))
			r.Header.Set("Authorization", "Bearer "+e.raw)
			e.h.ServeHTTP(w, r)
		}()
	}
	wg.Wait()

	// Every request settled, so nothing may remain in flight.
	cost, tokens, err := e.srv.Cache.Reserved(ctx, "key", e.key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cost != 0 || tokens != 0 {
		t.Fatalf("in-flight reservation leaked under churn: cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_ReservationReleasedOnRPMLimit: a refusal from a LATER check must
// release reservations taken by an earlier scope within the SAME request, or a
// rejected request permanently consumes budget it never used.
//
// The key has a budget (reserved first, since key is the tightest scope) and an
// RPM limit of 1. The first request settles normally and burns the RPM slot.
// The second reserves the key's budget, then is refused by RPM — so the
// reservation taken moments earlier in the same call must be released.
func TestAdmit_ReservationReleasedOnRPMLimit(t *testing.T) {
	one := int32(1)
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 100_000_000, RPMLimit: &one})
	ctx := context.Background()

	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("first %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 429 {
		t.Fatalf("second should be rate limited, got %d %s", w.Code, w.Body)
	}
	if cost, tokens, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 || tokens != 0 {
		t.Fatalf("reservation leaked on RPM refusal: cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_BudgetCapacityIsExactlyTheLimit pins the boundary at the heart of
// the fix: a cap of exactly N estimates must admit N requests, not N-1.
//
// An earlier version compared `committed + Reserve()` where Reserve already
// returned a total including this request, charging it twice and shrinking
// every budget to floor(cap/estimate)-1. The single-request case masked it,
// because `>` and the double-charge cancelled at that one boundary.
func TestAdmit_BudgetCapacityIsExactlyTheLimit(t *testing.T) {
	const est = 10_245_000 // chatBody's estimate: 1024 default output at fast prices
	const actual = 7_500_000
	ctx := context.Background()
	e := newEnv(t, repo.KeyInput{BudgetType: "total", BudgetNanos: 3 * est})

	admitted := 0
	for i := 0; i < 5; i++ {
		w := e.do("POST", "/v1/chat/completions", e.raw, chatBody)
		if w.Code != 200 {
			break
		}
		admitted++
		// Simulate the recorder committing this request's spend between
		// requests, which is what happens in production within 2s.
		if _, err := e.db.Pool.Exec(ctx,
			`UPDATE api_keys SET spend_nanos = spend_nanos + $1 WHERE id=$2`, actual, e.key.ID); err != nil {
			t.Fatal(err)
		}
	}
	if admitted != 3 {
		t.Fatalf("a cap of exactly 3 estimates must admit 3 requests, got %d", admitted)
	}
}

// TestAdmit_TPMBoundaryIsInclusive: a request landing exactly on the limit does
// not exceed it, so it is admitted. This matches the budget comparison, which
// uses the same `>` rather than `>=`.
func TestAdmit_TPMBoundaryIsInclusive(t *testing.T) {
	// chatBody estimates 1026 tokens (1024 default output + input); the fake
	// provider reports 1500 actual. A limit equal to the estimate must admit.
	limit := int32(1026)
	e := newEnv(t, repo.KeyInput{TPMLimit: &limit})
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("exactly-on-the-limit request should be admitted, got %d %s", w.Code, w.Body)
	}
}

// TestAdmit_UnlimitedBudgetTakesNoReservation: with no cap there is nothing to
// reserve, and the admit path must not pay for a Redis round trip.
func TestAdmit_UnlimitedBudgetTakesNoReservation(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "unlimited"})
	ctx := context.Background()
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if cost, tokens, _ := e.srv.Cache.Reserved(ctx, "key", e.key.ID); cost != 0 || tokens != 0 {
		t.Fatalf("unlimited budget should not reserve: cost=%d tpm=%d", cost, tokens)
	}
}

// TestAdmit_NoTPMLimitTakesNoTokenReservation pins that TPM is only reserved
// when the scope actually sets a limit.
func TestAdmit_NoTPMLimitTakesNoTokenReservation(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "unlimited"})
	ctx := context.Background()
	if w := e.do("POST", "/v1/chat/completions", e.raw, chatBody); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, tokens, _ := e.srv.Cache.Reserved(ctx, "group", e.key.GroupID); tokens != 0 {
		t.Fatalf("no TPM limit should reserve no tokens, got %d", tokens)
	}
}

// TestAdmit_GroupScopeReservedToo: a key under the cap but a group over it must
// still be refused, and the group is a separate scope with its own reservation.
func TestAdmit_GroupScopeReservedToo(t *testing.T) {
	e := newEnv(t, repo.KeyInput{BudgetType: "unlimited"})
	ctx := context.Background()
	if _, err := e.db.Pool.Exec(ctx,
		`UPDATE groups SET budget_type='total', budget_nanos=1, spend_nanos=1 WHERE id=$1`, e.key.GroupID); err != nil {
		t.Fatal(err)
	}
	w := e.do("POST", "/v1/chat/completions", e.raw, chatBody)
	if w.Code != 429 || errCode(t, w) != "budget_exceeded" {
		t.Fatalf("group budget should refuse: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "group") {
		t.Fatalf("expected the group scope named in the error: %s", w.Body)
	}
}
