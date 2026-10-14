// Package webhook delivers budget-crossing notifications to a single,
// admin-configured URL. It is deliberately the last hop in the spend path: it
// must never block recording, and every failure is logged and dropped, never
// returned to the recorder.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"tollgate/core/db/repo"
)

// Defaults are tuned for a best-effort notification, not a billing path: a few
// quick retries with short backoff, bounded, so a dead endpoint can't build up
// work. sendTimeout bounds each attempt; the http.Client's own timeout also
// applies as a ceiling.
var (
	sendTimeout  = 5 * time.Second
	maxAttempts  = 3
	retryBackoff = 200 * time.Millisecond // base; doubles each retry
	bufferSize   = 256
)

// Sender delivers budget crossings to the URL held in server_settings. The URL
// is read at send time (not at Submit), so changing the setting takes effect on
// the next crossing without a restart.
type Sender struct {
	settings func(ctx context.Context) (string, error) // budget_webhook_url
	client   *http.Client
	ch       chan repo.BudgetCrossing
}

// NewSender builds a Sender backed by the settings store. client supplies the
// transport; it should carry a timeout.
func NewSender(store *repo.Store, client *http.Client) *Sender {
	return &Sender{
		settings: func(ctx context.Context) (string, error) {
			st, err := store.GetSettings(ctx)
			if err != nil {
				return "", err
			}
			return st.BudgetWebhookURL, nil
		},
		client: client,
		ch:     make(chan repo.BudgetCrossing, bufferSize),
	}
}

// Submit enqueues a crossing for delivery and returns immediately. It never
// blocks: if the buffer is full it drops the crossing with a log so the spend
// path is never stalled.
func (s *Sender) Submit(_ context.Context, c repo.BudgetCrossing) {
	select {
	case s.ch <- c:
	default:
		slog.Warn("webhook buffer full; dropping budget crossing",
			"scope", c.Scope, "label", c.Label, "threshold", c.Threshold)
	}
}

// Run drains the queue until ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-s.ch:
			s.deliver(ctx, c)
		}
	}
}

// deliver POSTs the crossing with a bounded retry. Every failure is logged and
// swallowed; it is never returned to the caller.
func (s *Sender) deliver(ctx context.Context, c repo.BudgetCrossing) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := s.sendOnce(ctx, c)
		switch {
		case err == nil || ctx.Err() != nil:
			return
		case attempt == maxAttempts:
			slog.Error("budget webhook failed after retries",
				"scope", c.Scope, "label", c.Label, "attempts", attempt, "err", err)
			return
		}
		slog.Warn("budget webhook attempt failed; retrying",
			"scope", c.Scope, "label", c.Label, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryBackoff * time.Duration(1<<uint(attempt-1))):
		}
	}
}

// sendOnce makes one POST to the configured URL. It returns nil on a 2xx
// response and when no URL is configured (nothing to do is not a failure);
// anything else is an error to be retried.
func (s *Sender) sendOnce(ctx context.Context, c repo.BudgetCrossing) error {
	u, err := s.settings(ctx)
	if err != nil {
		return fmt.Errorf("read budget_webhook_url: %w", err)
	}
	if u == "" {
		slog.Debug("budget crossing not delivered: budget_webhook_url is empty",
			"scope", c.Scope, "label", c.Label)
		return nil
	}
	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("webhook returned %s", resp.Status)
}
