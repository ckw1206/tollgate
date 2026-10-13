package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func inAny(ip netip.Addr, nets []netip.Prefix) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP honors X-Forwarded-For only when the direct peer is a trusted proxy,
// and then takes the rightmost address that isn't itself a trusted proxy.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !inAny(peer.Unmap(), trusted) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !inAny(ip.Unmap(), trusted) {
			return ip.String()
		}
	}
	return host
}
