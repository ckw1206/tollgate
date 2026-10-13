package consoleapi

import (
	"log/slog"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// dummyHash makes unknown-email logins as slow as wrong-password ones, so
// response time doesn't reveal which emails have accounts.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("tollgate-dummy-password"), 12)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	ip := auth.ClientIP(r, s.TrustedProxies)
	if blocked, err := s.Cache.LoginBlocked(r.Context(), ip, in.Email); err != nil || blocked {
		if err != nil {
			slog.Error("login throttle", "err", err)
		}
		api.WriteError(w, 429, "rate_limit_error", "too_many_attempts", "too many failed attempts; try again in 15 minutes")
		return
	}
	u, err := s.Store.GetUserByEmail(r.Context(), in.Email)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(in.Password))
	}
	if err != nil || u.Disabled || !repo.CheckPassword(u, in.Password) {
		s.Cache.RecordLoginFailure(r.Context(), ip, in.Email)
		api.WriteError(w, 401, "authentication_error", "invalid_credentials", "invalid email or password")
		return
	}
	s.Cache.ClearLoginFailures(r.Context(), in.Email)
	if err := s.Sessions.Start(r.Context(), w, u.ID); err != nil {
		storeErr(w, err)
		return
	}
	s.writeMe(w, r, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Sessions.End(w, r)
	w.WriteHeader(204)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) { s.writeMe(w, r, auth.UserFrom(r.Context())) }

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, u *repo.User) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, map[string]any{"user": u, "permissions": rbac.Permissions(u.Role), "server": st})
}
