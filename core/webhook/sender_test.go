package webhook

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tollgate/core/db/repo"
)

// crossing builds a realistic payload. Label is a key alias/group name — the
// only human-readable identity the payload ever carries.
func crossing() repo.BudgetCrossing {
	reset := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	return repo.BudgetCrossing{
		Scope:         "key",
		ID:            "11111111-2222-3333-4444-555555555555",
		Label:         "kyle-prod",
		Threshold:     80,
		SpendNanos:    800_000_000,
		BudgetNanos:   1_000_000_000,
		BudgetType:    "daily",
		BudgetResetAt: &reset,
	}
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, after time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.After(after)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			return false
		case <-tick.C:
			if cond() {
				return true
			}
		}
	}
}

// newTestSender assembles a Sender the way tests have done all along: a fixed
// URL, a test client, a small buffer, and a fresh done channel so Run's
// completion can be observed.
func newTestSender(url string, client *http.Client) *Sender {
	return &Sender{
		settings: func(context.Context) (string, error) { return url, nil },
		client:   client,
		ch:       make(chan repo.BudgetCrossing, 16),
		done:     make(chan struct{}),
	}
}

// noRedirectClient is what production configures on webhookClient: redirects
// are not followed, so a 3xx response is delivered to sendOnce as-is.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// logSink is a mutex-guarded buffer so the test goroutine can read the logs
// while the sender goroutine is still writing to them through the slog handler.
type logSink struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureSlog points the default slog logger at a thread-safe buffer for the
// duration of the test. Tests in this package run sequentially, so a global
// swap is safe.
func captureSlog(t *testing.T) *logSink {
	t.Helper()
	sink := &logSink{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(sink, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return sink
}

func TestSender_DeliversExactPayload(t *testing.T) {
	var got atomic.Value // *[]byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		body := make([]byte, 0, 1<<16)
		buf := make([]byte, 1<<16)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil {
				break
			}
		}
		cp := append([]byte(nil), body...)
		got.Store(cp)
		w.WriteHeader(200)
	}))
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSender(up.URL, up.Client())
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	if !waitFor(t, 2*time.Second, func() bool { return got.Load() != nil }) {
		t.Fatal("no delivery")
	}

	var m map[string]any
	raw := string(got.Load().([]byte))
	if err := json.Unmarshal(got.Load().([]byte), &m); err != nil {
		t.Fatalf("payload not JSON: %v: %s", err, raw)
	}
	want := []string{"scope", "id", "label", "threshold", "spend_nanos", "budget_nanos", "budget_type", "budget_reset_at"}
	if len(m) != len(want) {
		t.Fatalf("payload has %d fields %v, want exactly %v", len(m), m, want)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in %v", k, m)
		}
	}
	// No raw key, hash, or prefix may leak into the payload. Only the alias
	// label and the UUID id are human/machine identifiers.
	for _, bad := range []string{"sk-", "hash", "prefix", "key_material", "ciphertext", "nonce"} {
		if strings.Contains(strings.ToLower(raw), bad) {
			t.Errorf("payload leaks %q: %s", bad, raw)
		}
	}
	// Values round-trip.
	if m["label"] != "kyle-prod" || m["threshold"] != 80.0 || m["spend_nanos"] != 800_000_000.0 ||
		m["budget_nanos"] != 1_000_000_000.0 || m["budget_type"] != "daily" {
		t.Errorf("payload values wrong: %v", m)
	}
	s.Close()
	<-s.Done()
}

// TestSender_RetriesBoundedThenGivesUp: a 500 endpoint is hit exactly
// maxAttempts times, then the crossing is dropped (Run keeps serving).
func TestSender_RetriesBoundedThenGivesUp(t *testing.T) {
	orig := retryBackoff
	retryBackoff = time.Millisecond
	defer func() { retryBackoff = orig }()

	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSender(up.URL, up.Client())
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	if !waitFor(t, 3*time.Second, func() bool { return hits.Load() == int32(maxAttempts) }) {
		t.Fatalf("hits = %d, want %d", hits.Load(), maxAttempts)
	}
	// No further retries after giving up: the count must stay put.
	time.Sleep(50 * time.Millisecond)
	if h := hits.Load(); h != int32(maxAttempts) {
		t.Fatalf("retry count kept growing: %d, want %d", h, maxAttempts)
	}
	s.Close()
	<-s.Done()
}

// TestSender_EmptyURLIsNoOp: with no configured URL, a crossing is not POSTed
// and is not an error.
func TestSender_EmptyURLIsNoOp(t *testing.T) {
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSender("", up.Client())
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	time.Sleep(30 * time.Millisecond)
	if h := hits.Load(); h != 0 {
		t.Fatalf("empty URL should not POST, got %d hits", hits.Load())
	}
	s.Close()
	<-s.Done()
}

// TestSender_SubmitNeverBlocks: when the buffer is full and no consumer runs,
// Submit drops instead of blocking the (hypothetical) spend path.
func TestSender_SubmitNeverBlocks(t *testing.T) {
	ch := make(chan repo.BudgetCrossing, bufferSize)
	// Fill the buffer so every new Submit hits the drop path.
	for i := 0; i < bufferSize; i++ {
		ch <- crossing()
	}
	s := &Sender{settings: func(context.Context) (string, error) { return "", nil },
		client: http.DefaultClient, ch: ch}

	done := make(chan struct{})
	go func() {
		s.Submit(context.Background(), crossing())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked on a full buffer")
	}
	// The buffer is still exactly full (the new crossing was dropped).
	if len(ch) != bufferSize {
		t.Fatalf("buffer len = %d, want %d (drop, not overflow)", len(ch), bufferSize)
	}
}

// TestSender_RedirectIsAFailureNotDelivery: a 302 must not count as delivered.
// With production's CheckRedirect = http.ErrUseLastResponse the 3xx response
// comes back un-followed; sendOnce treats it as an error, so the sender
// retries maxAttempts times, gives up, and the payload is never POSTed as a
// GET to the follow-up location.
func TestSender_RedirectIsAFailureNotDelivery(t *testing.T) {
	orig := retryBackoff
	retryBackoff = time.Millisecond
	defer func() { retryBackoff = orig }()

	var mu sync.Mutex
	var methods []string
	var paths []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.Redirect(w, r, "/followed", http.StatusFound) // 302
	}))
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := captureSlog(t)
	s := newTestSender(up.URL, noRedirectClient())
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	if !waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(methods) == maxAttempts
	}) {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("endpoint hit %d times %v, want %d", len(methods), methods, maxAttempts)
	}
	// Every attempt must be a POST to the original path — never a GET to the
	// redirect target — and the failure must be logged as a failure.
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	if len(methods) != maxAttempts {
		t.Fatalf("endpoint hit %d times, want %d (no extra follow-ups)", len(methods), maxAttempts)
	}
	for i, m := range methods {
		if m != http.MethodPost {
			t.Errorf("attempt %d method = %s, want POST (redirect must not be followed)", i, m)
		}
		if paths[i] != "/" {
			t.Errorf("attempt %d path = %q, want the original path, not the redirect target", i, paths[i])
		}
	}
	mu.Unlock()
	if !waitFor(t, 2*time.Second, func() bool { return strings.Contains(logs.String(), "failed after retries") }) {
		t.Errorf("redirect was not logged as a failure:\n%s", logs.String())
	}
	s.Close()
	<-s.Done()
}

// TestSender_RequestErrorDoesNotLeakWebhookURL: the URL's path IS the
// credential for Slack/Discord-style webhooks. A request error (*url.Error)
// carries the full URL; scrubRequestError must strip it so no log line ever
// contains the token.
func TestSender_RequestErrorDoesNotLeakWebhookURL(t *testing.T) {
	origBackoff, origTimeout := retryBackoff, sendTimeout
	retryBackoff, sendTimeout = time.Millisecond, time.Second
	defer func() { retryBackoff, sendTimeout = origBackoff, origTimeout }()

	const token = "S0b4c1d2e3f4a5b6c7d8e9f0a1b2c3d4"
	url := "http://127.0.0.1:1/hooks/" + token // port 1: refused, so every attempt fails with a *url.Error

	logs := captureSlog(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSender(url, http.DefaultClient)
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	if !waitFor(t, 5*time.Second, func() bool { return strings.Contains(logs.String(), "failed after retries") }) {
		t.Fatalf("sender never finished giving up:\n%s", logs.String())
	}
	// Stop the sender before the test ends so nothing writes to the log
	// buffer (or the swapped default logger) from behind.
	s.Close()
	<-s.Done()
	out := logs.String()
	if strings.Contains(out, token) {
		t.Errorf("webhook URL path token leaked into logs:\n%s", out)
	}
	if strings.Contains(out, "/hooks/") {
		t.Errorf("webhook URL path leaked into logs:\n%s", out)
	}
	// The underlying dial error must still be visible for debugging.
	if !strings.Contains(out, "connection refused") && !strings.Contains(out, "dial") {
		t.Errorf("expected the scrubbed dial error in logs:\n%s", out)
	}
}

// TestSender_CloseDrainsQueue: after Close, Run delivers everything still
// buffered before returning. This is the shutdown path: a crossing Submited by
// the recorder's final flush must reach the endpoint even though no more
// crossings will ever come.
func TestSender_CloseDrainsQueue(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 0, 1<<16)
		tmp := make([]byte, 1<<16)
		for {
			n, err := r.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		mu.Lock()
		bodies = append(bodies, string(buf))
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer up.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := newTestSender(up.URL, up.Client())
	go s.Run(ctx)

	// First crossing: delivered while the queue is live.
	s.Submit(ctx, crossing())
	if !waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(bodies) == 1
	}) {
		t.Fatal("first crossing was not delivered")
	}

	// Second crossing: enqueued right before shutdown, like the recorder's
	// final flush. Close must drain it, and Run must then return.
	s.Submit(ctx, crossing())
	s.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Close drained the queue")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("delivered %d crossings after shutdown, want 2", len(bodies))
	}
	if !strings.Contains(bodies[1], "kyle-prod") {
		t.Errorf("drained payload wrong: %s", bodies[1])
	}
}

// TestSender_SubmitAfterCloseNoPanic: Submits racing or following Close must
// be dropped, never panicked on a closed channel, and never leak a goroutine.
func TestSender_SubmitAfterCloseNoPanic(t *testing.T) {
	// No Run at all: Close first, then Submits.
	s := &Sender{settings: func(context.Context) (string, error) { return "", nil },
		client: http.DefaultClient, ch: make(chan repo.BudgetCrossing, 4), done: make(chan struct{})}
	s.Close()
	for i := 0; i < 10; i++ {
		s.Submit(context.Background(), crossing()) // dropped; must not panic or block
	}
	s.Close() // idempotent

	// With Run: cancel, wait for Run to exit, then Submit and Close.
	s2 := newTestSender("", http.DefaultClient)
	ctx, cancel := context.WithCancel(context.Background())
	go s2.Run(ctx)
	cancel()
	select {
	case <-s2.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on cancel (goroutine leak)")
	}
	s2.Submit(context.Background(), crossing()) // after drain: dropped, no panic
	s2.Close()
}

// TestSender_CancelMidDrainReturnsPromptly: the shutdown cap in main.go relies
// on Run returning promptly when its ctx is cancelled MID-DRAIN: the in-flight
// client.Do must be aborted (its request context derives from the sender's
// ctx) and the remaining queue dropped without blocking. With a long
// per-attempt timeout a full drain against this endpoint would take
// queued x maxAttempts x sendTimeout (450s), so returning in well under that
// can only mean the cancel aborted the in-flight request. The endpoint accepts
// and never responds, so the first request is genuinely in progress at cancel
// time.
func TestSender_CancelMidDrainReturnsPromptly(t *testing.T) {
	origTimeout, origBackoff := sendTimeout, retryBackoff
	sendTimeout, retryBackoff = 30*time.Second, time.Millisecond
	defer func() { sendTimeout, retryBackoff = origTimeout, origBackoff }()

	var hits atomic.Int32
	// A raw listener that accepts and then holds the connection open without
	// ever responding — simpler than an HTTP server here, because an HTTP
	// handler blocked on request-body reads stays alive on a half-closed
	// connection (its context is not cancelled by client-side abort) and
	// would block listener close at the end of the test.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			// Never read, never respond: the request stays in flight
			// until the test closes the listener, dropping the connection.
			_ = conn
		}
	}()

	const queued = 5
	ctx, cancel := context.WithCancel(context.Background())
	s := newTestSender("http://"+l.Addr().String(), http.DefaultClient)
	go s.Run(ctx)
	for i := 0; i < queued; i++ {
		s.Submit(ctx, crossing())
	}
	// The first delivery must be genuinely in flight before we cancel.
	if !waitFor(t, 3*time.Second, func() bool { return hits.Load() >= 1 }) {
		t.Fatal("no in-flight request reached the endpoint before cancel")
	}
	cancel()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after ctx cancel mid-drain (in-flight request not aborted?)")
	}
	// After the cancel: Submits must still drop silently (never panic) and
	// Close after cancel must be a no-op, not a panic.
	for i := 0; i < queued; i++ {
		s.Submit(context.Background(), crossing())
	}
	s.Close()
}

// TestSender_SubmitRacesClose: Submit and Close from separate goroutines must
// never deadlock or panic (run under -race).
func TestSender_SubmitRacesClose(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := &Sender{settings: func(context.Context) (string, error) { return "", nil },
			client: http.DefaultClient, ch: make(chan repo.BudgetCrossing, 8), done: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); s.Submit(ctx, crossing()) }()
		go func() { defer wg.Done(); s.Close() }()
		wg.Wait()
		cancel()
	}
}
