package webhook

import (
	"net/http"
	"time"
)

// NewWebhookClient builds the *http.Client used to deliver budget crossings —
// the SAME construction main.go wires into NewSender. Tests must exercise the
// client this constructor returns (not a hand-built copy), so production
// behavior (SSRF-guarded transport, no redirect following, timeout) is pinned
// by the test suite rather than drifting silently.
func NewWebhookClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		// Transport whose dialer runs Control over every resolved address —
		// the SSRF guard (the webhook URL is admin-configured).
		Transport: SafeTransport(),
		// No redirect following: a 3xx must fail loudly (sendOnce treats it
		// as a delivery failure and retries), never be chased as a bodyless
		// GET that drops the payload.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
