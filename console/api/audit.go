package consoleapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"tollgate/console/auth"
	"tollgate/core/db/repo"
)

// audit records a console mutation. before/after are marshalled with their
// json tags, which already exclude password hashes, key hashes, and ciphertext.
func (s *Server) audit(r *http.Request, action, resource, id string, before, after any) {
	details, _ := json.Marshal(map[string]any{"before": before, "after": after})
	e := repo.AuditEntry{Action: action, Resource: resource, ResourceID: id, IP: auth.ClientIP(r, s.TrustedProxies), Details: details}
	if u := auth.UserFrom(r.Context()); u != nil {
		e.ActorID, e.ActorEmail = &u.ID, u.Email
	}
	if err := s.Store.InsertAudit(r.Context(), e); err != nil {
		slog.Error("audit write failed", "action", action, "err", err)
	}
}
