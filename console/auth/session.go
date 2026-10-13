// Package auth handles console sessions and request hygiene.
package auth

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/cache"
	"tollgate/core/db/repo"
)

const CookieName = "tollgate_session"

type Sessions struct {
	Cache    *cache.Cache
	Store    *repo.Store
	Insecure bool // COOKIE_INSECURE: drop the Secure flag for plain-http local dev
	TTL      time.Duration
}

type userKey struct{}

func UserFrom(ctx context.Context) *repo.User {
	u, _ := ctx.Value(userKey{}).(*repo.User)
	return u
}

func (s *Sessions) Start(ctx context.Context, w http.ResponseWriter, userID string) error {
	token := repo.RandomToken(32)
	if err := s.Cache.SetSession(ctx, repo.HashToken(token), userID, s.TTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: !s.Insecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(s.TTL.Seconds())})
	return nil
}

func (s *Sessions) End(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.Cache.DelSession(r.Context(), repo.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: !s.Insecure, MaxAge: -1})
}

// Load attaches the session user to the context. The user row is re-read on
// every request so role changes and disables apply immediately.
func (s *Sessions) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		id, err := s.Cache.GetSession(r.Context(), repo.HashToken(c.Value))
		if err != nil {
			slog.Error("session lookup", "err", err)
		}
		if id != "" {
			if u, err := s.Store.GetUser(r.Context(), id); err == nil && !u.Disabled {
				r = r.WithContext(context.WithValue(r.Context(), userKey{}, u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Sessions) Require(p rbac.Permission, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFrom(r.Context())
		if u == nil {
			api.WriteError(w, 401, "authentication_error", "not_signed_in", "sign in required")
			return
		}
		if p != "" && !rbac.Has(u.Role, p) {
			api.WriteError(w, 403, "permission_error", "forbidden", "you don't have permission to do that")
			return
		}
		h(w, r)
	})
}
