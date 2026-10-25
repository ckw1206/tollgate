package api

import (
	"net/http"

	"tollgate/core/metrics"
)

// metricsHandler serves the admit-path instrumentation added for issue #33.
//
// # Why this route is UNauthenticated
//
// It carries no secrets and no per-tenant data: the document is a fixed set of
// operation names ("auth_lookup", "reserve_add", ...) with counts and latency
// buckets. There is no key, token, user, group or model identifier anywhere in
// it — that is enforced by construction, since metrics.Op is a closed
// enumeration and metrics.Observe takes nothing else.
//
// It is reachable without a credential for the same reason GET /health is:
// both are operational endpoints on the same port as the gateway, and the
// deployment is explicitly single-instance and self-hosted (CLAUDE.md), so the
// port is not a shared or public multi-tenant surface. Requiring a console
// session would also make the trigger unreadable during exactly the incident
// where someone needs it (an auth-path slowdown, when signing in is what is
// broken). A future console view over the same numbers, if wanted, is a
// separate change with its own permission.
//
// What it must never grow into: any series keyed by scope ID, key hash or
// token. If a future change needs per-key latency, that is a new decision with
// its own auth model, not an extension of this handler.
//
// Cache-Control: no-store because a cached copy of this document is actively
// harmful. Its one job is deciding whether the lookup is slow NOW, and every
// number in it is a lifetime or rolling-window value that changes on every
// request — a stale body would answer a different question than the one asked.
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, 200, metrics.Report())
}
