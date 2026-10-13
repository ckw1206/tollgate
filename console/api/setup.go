package consoleapi

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"tollgate/console/auth"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// SetupToken is the first-run setup secret. It lives only in memory: main creates
// one at startup when there are no users and logs the setup URL once. A nil or
// discarded token accepts nothing.
type SetupToken struct {
	mu  sync.Mutex
	tok string
}

func NewSetupToken() (*SetupToken, string) {
	t := repo.RandomToken(32)
	return &SetupToken{tok: t}, t
}

func (s *SetupToken) Valid(t string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tok != "" && subtle.ConstantTimeCompare([]byte(s.tok), []byte(t)) == 1
}

func (s *SetupToken) Discard() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.tok = ""
	s.mu.Unlock()
}

func (s *Server) setupNeeded(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, map[string]bool{"needed": n == 0})
}

func alreadySetUp(w http.ResponseWriter) {
	api.WriteError(w, 409, "invalid_request_error", "already_set_up", "setup is already complete; sign in instead")
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Email, Name, Password string }
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	ip := auth.ClientIP(r, s.TrustedProxies)
	// Read the token before counting users: the winning setup discards it only after
	// its insert commits, so a concurrent loser that sees it discarded then also sees
	// that user and gets 409, never 403.
	valid := s.Setup.Valid(in.Token)
	n, err := s.Store.CountUsers(ctx)
	if err != nil {
		storeErr(w, err)
		return
	}
	// Once anyone exists the endpoint is inert: 409 before the throttle, no writes.
	if n > 0 {
		s.Setup.Discard()
		alreadySetUp(w)
		return
	}
	if blocked, err := s.Cache.SetupBlocked(ctx, ip); err != nil || blocked {
		if err != nil {
			slog.Error("setup throttle", "err", err)
		}
		api.WriteError(w, 429, "rate_limit_error", "too_many_attempts", "too many failed attempts; try again in 15 minutes")
		return
	}
	if !valid {
		s.Cache.RecordSetupFailure(ctx, ip) // setup's own IP counter, never the sign-in counters
		api.WriteError(w, 403, "permission_error", "invalid_setup_token", "invalid setup token")
		return
	}
	in.Email, in.Name = strings.TrimSpace(in.Email), strings.TrimSpace(in.Name)
	if !strings.Contains(in.Email, "@") {
		badRequest(w, "a valid email is required")
		return
	}
	if in.Name == "" {
		badRequest(w, "name is required")
		return
	}
	if len(in.Password) < 12 {
		badRequest(w, "password must be at least 12 characters")
		return
	}
	u, err := s.Store.BootstrapAdmin(ctx, in.Email, in.Name, in.Password)
	if errors.Is(err, repo.ErrAlreadySetUp) {
		s.Setup.Discard()
		alreadySetUp(w)
		return
	} else if err != nil {
		storeErr(w, err)
		return
	}
	s.Setup.Discard()
	if err := s.Sessions.Start(ctx, w, u.ID); err != nil {
		storeErr(w, err)
		return
	}
	e := repo.AuditEntry{ActorID: &u.ID, ActorEmail: u.Email, Action: "user.bootstrap", Resource: "users", ResourceID: u.ID, IP: ip}
	if err := s.Store.InsertAudit(ctx, e); err != nil {
		slog.Error("audit write failed", "action", e.Action, "err", err)
	}
	api.WriteJSON(w, 201, u)
}

// landing is the public branding the sign-in and setup pages show before anyone logs in.
func (s *Server) landing(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	api.WriteJSON(w, 200, map[string]string{
		"server_name": st.ServerName, "server_icon": st.ServerIcon,
		"landing_headline": st.LandingHeadline, "landing_tagline": st.LandingTagline,
		"version": s.Version, "commit": s.Commit,
	})
}
