package consoleapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// spendFilter parses from/to (RFC3339, default the last 30 days) and the filters, scoped to
// the caller's own spend without spend:read_all. ok is false once a 400 has been written.
func spendFilter(w http.ResponseWriter, r *http.Request) (f repo.SpendFilter, ok bool) {
	q := r.URL.Query()
	now := time.Now()
	f = repo.SpendFilter{From: now.AddDate(0, 0, -30), To: now.Add(time.Minute), GroupBy: q.Get("group_by"),
		UserID: q.Get("user_id"), GroupID: q.Get("group_id"), KeyID: q.Get("key_id")}
	for name, dst := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				badRequest(w, name+" must be RFC3339")
				return f, false
			}
			*dst = t
		}
	}
	me := auth.UserFrom(r.Context())
	if !rbac.Has(me.Role, rbac.SpendReadAll) {
		f.UserID, f.GroupID = me.ID, ""
	}
	return f, true
}

func (s *Server) spend(w http.ResponseWriter, r *http.Request) {
	f, ok := spendFilter(w, r)
	if !ok {
		return
	}
	if f.GroupBy == "" {
		f.GroupBy = "day"
	}
	rows, err := s.Store.QuerySpend(r.Context(), f)
	if errors.Is(err, repo.ErrGroupBy) {
		badRequest(w, err.Error())
		return
	} else if err != nil {
		storeErr(w, err)
		return
	}
	if rows == nil {
		rows = []repo.SpendRow{}
	}
	api.WriteJSON(w, 200, map[string]any{"rows": rows})
}

// spendRequests: the newest requests in the range, limit 10 by default, at most 100.
func (s *Server) spendRequests(w http.ResponseWriter, r *http.Request) {
	f, ok := spendFilter(w, r)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = 10
	}
	logs, err := s.Store.RecentSpend(r.Context(), f, min(limit, 100))
	if err != nil {
		storeErr(w, err)
		return
	}
	if logs == nil {
		logs = []repo.SpendLog{}
	}
	api.WriteJSON(w, 200, logs)
}
