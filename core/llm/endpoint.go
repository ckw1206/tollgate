package llm

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"
)

// ValidateAPIBase checks a model or credential api_base: "" (provider default) or an
// http(s) URL without a trailing slash, credentials, query or fragment. Plain http is
// only allowed to private or loopback hosts.
func ValidateAPIBase(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
		return errors.New("api_base must be an http(s) URL")
	case u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return errors.New("api_base must not include credentials, a query or a fragment")
	case strings.HasSuffix(s, "/"):
		return errors.New("api_base must not end with a slash")
	case u.Scheme == "http" && !PrivateHost(u.Hostname()):
		return errors.New("api_base must use https unless the host is private or loopback")
	}
	return nil
}

// PrivateHost reports whether h is localhost or an IP literal in 10/8, 172.16/12,
// 192.168/16, 127/8 or ::1. Hostnames are not resolved.
func PrivateHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || (ip.Is4() && ip.IsPrivate())
}
