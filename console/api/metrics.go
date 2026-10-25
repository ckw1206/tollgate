package consoleapi

import (
	"net/http"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/metrics"
)

// metrics returns the admit-path instrumentation added for issue #33.
//
// This is the read the gateway itself does NOT expose: core/api's GET /metrics
// is unauthenticated on the gateway port, and this is the same document behind
// a console session with metrics:read, for a human looking at a dashboard host
// that cannot reach the gateway port. Both call metrics.Report(), so the two
// can never drift.
//
// The permission is admin-only by construction: metrics:read is held by neither
// viewer nor member (console/rbac/rbac.go), and TestMetrics_MemberAndViewerDenied
// pins that. A comment here is not the check — rbac.Has in Sessions.Require is.
//
// The document is aggregate-only. It must never gain a series keyed by key
// hash, token, user or group ID: see core/api/metrics.go for why, and note that
// such a change would also make this route's payload tenant data rather than
// fleet data.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	if me == nil || !rbac.Has(me.Role, rbac.MetricsRead) {
		api.WriteError(w, 403, "permission_error", "forbidden", "you don't have permission to do that")
		return
	}
	// Same reasoning as the gateway route: a cached copy would answer a
	// different question than the one being asked (see core/api/metrics.go).
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, 200, metrics.Report())
}
