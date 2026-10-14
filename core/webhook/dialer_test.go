package webhook

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestControl_RefusesInternalAddresses: every address range the guard must
// keep out of webhook delivery — loopback (127/8, ::1), the link-local cloud
// metadata range (169.254/16, fe80::/10), RFC1918 private space, RFC4193
// unique-local (fc00::/7), multicast, and unspecified.
func TestControl_RefusesInternalAddresses(t *testing.T) {
	bad := []string{
		"127.0.0.1:443",
		"127.255.0.1:80",
		"169.254.169.254:80", // cloud metadata endpoint
		"10.255.255.1:80",
		"172.16.0.1:443",
		"192.168.1.1:80",
		"[::1]:443",
		"[fe80::1]:443",
		"[fc00::1]:443",
		"[fd12:3456::1]:443",
		"0.0.0.0:80",
		"[::]:80",
		"224.0.0.1:80",
	}
	for _, a := range bad {
		if err := Control("tcp", a, nil); err == nil {
			t.Errorf("Control(%q) = nil, want refusal", a)
		}
	}
}

// TestControl_AllowsPublicAddresses: public unicast IPs pass the check. The
// requirement is that Control itself accept them, not that we actually dial
// out — no external calls in tests.
func TestControl_AllowsPublicAddresses(t *testing.T) {
	good := []string{
		"8.8.8.8:443",
		"1.1.1.1:443",
		"[2001:4860:4860::8888]:443",
	}
	for _, a := range good {
		if err := Control("tcp", a, nil); err != nil {
			t.Errorf("Control(%q) = %v, want nil", a, err)
		}
	}
}

// TestControl_RefusesHostnamesResolvingInternally: DNS names are checked too —
// the dialer hands Control every address a host resolves to, so a name that
// resolves to loopback is refused just like a literal.
func TestControl_RefusesHostnamesResolvingInternally(t *testing.T) {
	ips, err := net.LookupIP("localhost")
	if err != nil {
		t.Skip("localhost does not resolve on this host")
	}
	for _, ip := range ips {
		if allowedIP(ip) {
			t.Skip("localhost resolves to a public IP on this host")
		}
	}
	if err := Control("tcp", "localhost:443", nil); err == nil {
		t.Error("Control accepted a hostname that resolves only to internal addresses")
	}
}

// TestSafeTransport_RefusesLoopbackDial: through the real transport, a request
// to loopback is refused by Control before any connection is attempted — the
// failure is the guard's, not a timeout.
func TestSafeTransport_RefusesLoopbackDial(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200) // never reached
	}))
	defer up.Close()
	c := &http.Client{Transport: SafeTransport(), Timeout: 5 * time.Second}
	resp, err := c.Get(up.URL) // up.URL is http://127.0.0.1:port
	if err == nil {
		resp.Body.Close()
		t.Fatal("dial to loopback succeeded; the SSRF guard did not refuse")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v, want the Control refusal", err)
	}
}

// TestSafeTransport_RefusesMetadataEndpointDial: 169.254.169.254 is refused by
// Control before any network attempt (no short-timeout dependency).
func TestSafeTransport_RefusesMetadataEndpointDial(t *testing.T) {
	c := &http.Client{Transport: SafeTransport(), Timeout: 5 * time.Second}
	_, err := c.Get("http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("dial to the cloud metadata endpoint succeeded; the SSRF guard did not refuse")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v, want the Control refusal", err)
	}
}
