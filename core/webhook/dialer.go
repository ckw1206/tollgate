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
// fe80::/10, multicast, and the unspecified addresses. On top of that two
// ranges net.IP has no helper for are checked explicitly:
//   - CGNAT 100.64.0.0/10 (RFC 6598, "shared address space"): some ISPs put
//     whole customer networks here, so a dial to it is internal traffic, not
//     the public internet.
//   - "this network" 0.0.0.0/8: the source-network range, including 0.0.0.0
//     itself — on Linux a connect to it routes to the local host.
//
// (netip has no IsCGNAT; the range is checked by hand.)
func allowedIP(ip net.IP) bool {
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified() &&
		!inCGNAT(ip) &&
		!inThisNetwork(ip)
}

// cgnatPrefix is 100.64.0.0/10 (RFC 6598 shared address space).
var cgnatPrefix = func() *net.IPNet {
	_, p, _ := net.ParseCIDR("100.64.0.0/10")
	return p
}()

func inCGNAT(ip net.IP) bool { return cgnatPrefix.Contains(ip) }

// thisNetPrefix is 0.0.0.0/8 ("this network on this host").
var thisNetPrefix = func() *net.IPNet {
	_, p, _ := net.ParseCIDR("0.0.0.0/8")
	return p
}()

func inThisNetwork(ip net.IP) bool { return thisNetPrefix.Contains(ip) }

// IsPublicHost reports whether a webhook destination host is a public IP
// (literal) or a name that resolves ONLY to public IPs, using exactly the same
// rules as Control (allowedIP). It resolves the host up front, so the console
// "Test" endpoint can reject a bad URL with a 400 before spending a dial.
//
// It returns a non-nil error when the host is not allowed (so the caller can
// still distinguish "private/unresolvable" from a valid host); the error is
// descriptive, never a panic. An unresolvable name is not public.
func IsPublicHost(host string) (bool, error) {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	var ips []net.IP
	if ip := net.ParseIP(h); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(h)
		if err != nil {
			return false, fmt.Errorf("resolve %q: %w", h, err)
		}
		ips = resolved
	}
	if len(ips) == 0 {
		return false, fmt.Errorf("%q resolved to no addresses", h)
	}
	for _, ip := range ips {
		if !allowedIP(ip) {
			return false, fmt.Errorf("host %q resolves to non-public address %s", h, ip)
		}
	}
	return true, nil
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
