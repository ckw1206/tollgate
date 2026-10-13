package llm

import (
	"errors"
	"net/url"
	"strings"
)

// ValidateAPIBase checks a model or credential api_base: "" (provider default) or an
// http(s) URL without a trailing slash, credentials, query or fragment.
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
	}
	return nil
}
