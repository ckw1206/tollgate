// Package consoleapi serves /auth/* and /console/api/v1/* for the web console.
package consoleapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/cache"
	"tollgate/core/db/repo"
	"tollgate/core/vault"
)

type Server struct {
	Store           *repo.Store
	Cache           *cache.Cache
	Sessions        *auth.Sessions
	Vault           *vault.Vault
	Gateway         *api.Server                 // the /v1 server: the playground runs its chat path through ChatAs
	Reload          func(context.Context) error // rebuild the model registry after model/credential writes
	BaseURL         string
	TrustedProxies  []netip.Prefix
	Version, Commit string      // build info for /auth/me, set by main from -ldflags
	Setup           *SetupToken // first-run setup token; nil when users existed at startup
}

const p = "/console/api/v1"

func (s *Server) Register(mux *http.ServeMux) {
	req := s.Sessions.Require
	routes := map[string]http.Handler{
		"POST /auth/login":                http.HandlerFunc(s.login),
		"POST /auth/logout":               req("", s.logout),
		"GET /auth/me":                    req("", s.me),
		"GET /auth/invite/{token}":        http.HandlerFunc(s.inviteInfo),
		"POST /auth/invite/{token}":       http.HandlerFunc(s.inviteAccept),
		"GET /auth/setup":                 http.HandlerFunc(s.setupNeeded),
		"POST /auth/setup":                http.HandlerFunc(s.setup),
		"GET /auth/landing":               http.HandlerFunc(s.landing),
		"GET " + p + "/invites":           req(rbac.InvitesRead, s.listInvites),
		"POST " + p + "/invites":          req(rbac.InvitesWrite, s.createInvite),
		"DELETE " + p + "/invites/{id}":   req(rbac.InvitesWrite, s.revokeInvite),
		"GET " + p + "/users":             req(rbac.UsersRead, s.listUsers),
		"POST " + p + "/users":            req(rbac.UsersWrite, s.createUser),
		"PUT " + p + "/users/{id}":        req(rbac.UsersWrite, s.updateUser),
		"DELETE " + p + "/users/{id}":     req(rbac.UsersWrite, s.disableUser),
		"GET " + p + "/groups":            req(rbac.GroupsReadOwn, s.listGroups),
		"POST " + p + "/groups":           req(rbac.GroupsWrite, s.createGroup),
		"PUT " + p + "/groups/{id}":       req(rbac.GroupsWrite, s.updateGroup),
		"GET " + p + "/keys":              req(rbac.KeysReadOwn, s.listKeys),
		"POST " + p + "/keys":             req(rbac.KeysWriteOwn, s.createKey),
		"PUT " + p + "/keys/{id}":         req(rbac.KeysWriteOwn, s.updateKey),
		"DELETE " + p + "/keys/{id}":      req(rbac.KeysWriteOwn, s.revokeKey),
		"POST " + p + "/keys/{id}/rotate": req(rbac.KeysWriteOwn, s.rotateKey),
	}
	for pattern, h := range s.extraRoutes() { // Task 16
		routes[pattern] = h
	}
	for pattern, h := range routes {
		mux.Handle(pattern, s.Sessions.Load(h))
	}
}

// decode reads a JSON body (max 1 MiB) into v, writing a 400 on failure.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		api.WriteError(w, 400, "invalid_request_error", "invalid_json", "invalid JSON body")
		return false
	}
	return true
}

func badRequest(w http.ResponseWriter, msg string) {
	api.WriteError(w, 400, "invalid_request_error", "invalid_request", msg)
}

func notFound(w http.ResponseWriter) {
	api.WriteError(w, 404, "invalid_request_error", "not_found", "not found")
}

// storeErr maps repo errors to HTTP; anything unexpected is logged and hidden.
func storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrNotFound):
		notFound(w)
	case errors.Is(err, repo.ErrConflict):
		api.WriteError(w, 409, "invalid_request_error", "conflict", "already exists or still in use")
	default:
		slog.Error("console store error", "err", err)
		api.WriteError(w, 500, "api_error", "internal_error", "internal error")
	}
}

func validBudget(t string) bool {
	return t == "unlimited" || t == "daily" || t == "monthly" || t == "total"
}
