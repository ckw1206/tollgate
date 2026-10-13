package consoleapi

import (
	"net/http"
	"time"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) spend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	f := repo.SpendFilter{From: now.AddDate(0, 0, -30), To: now.Add(time.Minute), GroupBy: q.Get("group_by"),
		UserID: q.Get("user_id"), GroupID: q.Get("group_id"), KeyID: q.Get("key_id")}
	if f.GroupBy == "" {
		f.GroupBy = "day"
	}
	for name, dst := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				badRequest(w, name+" must be RFC3339")
				return
			}
			*dst = t
		}
	}
	me := auth.UserFrom(r.Context())
	if !rbac.Has(me.Role, rbac.SpendReadAll) {
		f.UserID, f.GroupID = me.ID, ""
	}
	rows, err := s.Store.QuerySpend(r.Context(), f)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	recent, err := s.Store.RecentSpend(r.Context(), f, 50)
	if err != nil {
		storeErr(w, err)
		return
	}
	if rows == nil {
		rows = []repo.SpendRow{}
	}
	if recent == nil {
		recent = []repo.SpendLog{}
	}
	api.WriteJSON(w, 200, map[string]any{"rows": rows, "recent": recent})
}
