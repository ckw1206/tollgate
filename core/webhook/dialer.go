// SSRF guard for the webhook client. The webhook URL is admin-configured, so
// this is the one outbound request in the product aimed at a URL an operator
// can change at runtime; the dialer below makes sure it can never be turned
// into a probe of internal infrastructure (loopback, the link-local cloud
// metadata endpoint, the private LAN).
package webhook

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
)

// Control is the net.Dialer.Control function for the webhook client. It runs
// once for every destination address a connection would be made to and
// refuses internal ones. The resolver hands the dialer every address a host
// resolves to, so a dial is refused if ANY resolved address is disallowed.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("webhook dial: resolve %q: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if !allowedIP(ip) {
			return fmt.Errorf("webhook dial: destination %s is not allowed", ip)
		}
	}
	return nil
}

// allowedIP reports whether ip is a destination a webhook may be sent to:
// public unicast only. Everything the RFCs reserve for internal use is out —
// loopback (127/8, ::1), RFC1918 plus RFC4193 unique-local (fc00::/7) via
// IsPrivate, link-local including 169.254/16 (the cloud metadata range) and
// fe80::/10, multicast, and the unspecified addresses.
func allowedIP(ip net.IP) bool {
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified()
}

// SafeTransport returns the *http.Transport for outbound webhook requests: a
// clone of the default transport (so connection pooling is unchanged) whose
// dialer runs Control over every destination address.
func SafeTransport() *http.Transport {
	d := &net.Dialer{Control: Control}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = d.DialContext
	return tr
}
