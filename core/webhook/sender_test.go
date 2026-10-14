package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	s := &Sender{settings: func(context.Context) (string, error) { return up.URL, nil },
		client: up.Client(), ch: make(chan repo.BudgetCrossing, 16)}
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
	s := &Sender{settings: func(context.Context) (string, error) { return up.URL, nil },
		client: up.Client(), ch: make(chan repo.BudgetCrossing, 16)}
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
	s := &Sender{settings: func(context.Context) (string, error) { return "", nil },
		client: up.Client(), ch: make(chan repo.BudgetCrossing, 16)}
	go s.Run(ctx)

	s.Submit(ctx, crossing())
	time.Sleep(30 * time.Millisecond)
	if h := hits.Load(); h != 0 {
		t.Fatalf("empty URL should not POST, got %d hits", h)
	}
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
