package consoleapi

import (
	"net/http"
	"strconv"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// A given limit is clamped to [1,200] (the repo caps it); absent or malformed → the default 50.
	limit, err := strconv.Atoi(q.Get("limit"))
	if err == nil {
		limit = max(limit, 1)
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	entries, total, err := s.Store.ListAudit(r.Context(), repo.AuditFilter{ActorID: q.Get("actor_id"), Action: q.Get("action"), Limit: limit, Offset: offset})
	if err != nil {
		storeErr(w, err)
		return
	}
	if entries == nil {
		entries = []repo.AuditEntry{}
	}
	api.WriteJSON(w, 200, map[string]any{"entries": entries, "total": total})
}
