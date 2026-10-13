package consoleapi

import (
	"net/http"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.GetSettings(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, st)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in repo.Settings
	if !decode(w, r, &in) {
		return
	}
	if in.ServerName == "" || in.Currency == "" {
		badRequest(w, "server_name and currency are required")
		return
	}
	before, _ := s.Store.GetSettings(r.Context())
	after, err := s.Store.UpdateSettings(r.Context(), in)
	if err != nil {
		badRequest(w, err.Error()) // only failure mode besides DB down is an invalid timezone
		return
	}
	s.audit(r, "settings.update", "server_settings", "1", before, after)
	api.WriteJSON(w, 200, after)
}
