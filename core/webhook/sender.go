// Package webhook delivers budget-crossing notifications to a single,
// admin-configured URL. It is deliberately the last hop in the spend path: it
// must never block recording, and every failure is logged and dropped, never
// returned to the recorder.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
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
	done     chan struct{} // closed when Run returns

	// mu guards closed and the send in Submit, so a Submit racing Close can
	// never send on the closed channel (which would panic).
	mu     sync.Mutex
	closed bool
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
		done:   make(chan struct{}),
	}
}

// Submit enqueues a crossing for delivery and returns immediately. It never
// blocks: if the buffer is full (or the sender is already closed) it drops the
// crossing with a log so the spend path is never stalled.
func (s *Sender) Submit(_ context.Context, c repo.BudgetCrossing) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		slog.Warn("webhook sender closed; dropping budget crossing",
			"scope", c.Scope, "label", c.Label, "threshold", c.Threshold)
		return
	}
	select {
	case s.ch <- c:
	default:
		slog.Warn("webhook buffer full; dropping budget crossing",
			"scope", c.Scope, "label", c.Label, "threshold", c.Threshold)
	}
}

// Close stops accepting crossings and closes the delivery channel so Run can
// drain what is already queued and return. Submits racing or following Close
// are dropped, never panicked. Idempotent; safe to call whether or not Run is
// running. Call it (and wait on Done) at shutdown so crossings from the final
// flush are delivered before the process exits.
func (s *Sender) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// Done is closed when Run returns.
func (s *Sender) Done() <-chan struct{} { return s.done }

// Run drains the queue until ctx is cancelled, or Close has closed the channel
// and it is empty.
func (s *Sender) Run(ctx context.Context) {
	if s.done != nil {
		defer close(s.done)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-s.ch:
			if !ok {
				return // channel closed and drained
			}
			s.deliver(ctx, c)
		}
	}
}

// deliver POSTs the crossing with a bounded retry. Every failure is logged and
// swallowed; it is never returned to the caller. Request errors are scrubbed
// before logging: a *url.Error carries the full request URL, and the webhook
// URL's path is itself a credential (Slack/Discord incoming webhooks).
func (s *Sender) deliver(ctx context.Context, c repo.BudgetCrossing) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := scrubRequestError(s.sendOnce(ctx, c))
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

// scrubRequestError unwraps a *url.Error so the error that reaches the logs
// does not include the request URL. net/http redacts only user:pass from the
// URL in its error text; a path token (the Slack/Discord webhook secret) would
// survive, so the URL is stripped entirely.
func scrubRequestError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// sendOnce makes one POST to the configured URL. It returns nil on a 2xx
// response and when no URL is configured (nothing to do is not a failure);
// anything else is an error to be retried. A 3xx is a failure, not a
// delivery: the client is configured with CheckRedirect =
// http.ErrUseLastResponse, so a redirect arrives here un-followed. Following
// it would make Go resend the POST as a GET with no body and the payload would
// be silently lost.
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
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("webhook returned redirect %s (not followed)", resp.Status)
	default:
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
}
