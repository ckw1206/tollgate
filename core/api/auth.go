package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tollgate/core/db/repo"
	"tollgate/core/metrics"
)

type authKey struct{}

func authFrom(ctx context.Context) *repo.AuthInfo { return ctx.Value(authKey{}).(*repo.AuthInfo) }

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			WriteError(w, 401, "authentication_error", "invalid_api_key", "missing API key")
			return
		}
		// Instrumentation only (#33) — the lookup below is unchanged and
		// uncached. The timing is deliberately NOT stored in the request
		// context: nothing downstream reads it, and a context value would keep
		// the measurement alive for the life of a stream for no reason.
		start := time.Now()
		info, err := s.Store.AuthByKeyHash(r.Context(), repo.HashToken(token))
		metrics.Observe(metrics.OpAuthLookup, time.Since(start))
		if errors.Is(err, repo.ErrNotFound) {
			WriteError(w, 401, "authentication_error", "invalid_api_key", "invalid API key")
			return
		}
		if err != nil {
			slog.Error("key lookup", "err", err)
			WriteError(w, 500, "api_error", "internal_error", "internal error")
			return
		}
		switch {
		case info.Key.RevokedAt != nil || info.UserDisabled:
			WriteError(w, 403, "permission_error", "key_blocked", "API key is revoked or its owner is disabled")
			return
		case info.Key.ExpiresAt != nil && s.Now().After(*info.Key.ExpiresAt):
			WriteError(w, 403, "permission_error", "key_expired", "API key has expired")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authKey{}, info)))
	})
}
