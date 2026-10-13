package auth

import (
	"mime"
	"net/http"
	"strings"

	"tollgate/core/api"
)

// CSRF guards cookie-authenticated routes: state-changing requests must be JSON
// (a cross-site HTML form can't send that) and, if the browser sends Origin, it
// must be ours. /v1 uses API keys, not cookies, so it's exempt.
func CSRF(baseURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieRoute := strings.HasPrefix(r.URL.Path, "/auth/") || strings.HasPrefix(r.URL.Path, "/console/")
		safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
		if cookieRoute && !safe {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			origin := r.Header.Get("Origin")
			if mt != "application/json" || (origin != "" && origin != baseURL) {
				api.WriteError(w, 403, "permission_error", "csrf", "cross-site request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
