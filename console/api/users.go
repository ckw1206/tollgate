package consoleapi

import (
	"net/http"
	"slices"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	us, err := s.Store.ListUsers(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, us)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in repo.UserUpdate
	if !decode(w, r, &in) {
		return
	}
	if in.Role != nil && !rbac.ValidRole(*in.Role) {
		badRequest(w, "role must be admin, member, or viewer")
		return
	}
	if !s.knownModels(w, r, in.ExtraModels) {
		return
	}
	me := auth.UserFrom(r.Context())
	if id == me.ID && ((in.Role != nil && *in.Role != "admin") || (in.Disabled != nil && *in.Disabled)) {
		badRequest(w, "you can't demote or disable yourself")
		return
	}
	if id == me.ID && ((in.GroupID != nil && *in.GroupID != me.GroupID) || (in.ExtraModels != nil && !slices.Equal(*in.ExtraModels, me.ExtraModels))) {
		badRequest(w, "you can't change your own group or model access")
		return
	}
	before, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	after, err := s.Store.UpdateUser(r.Context(), id, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "user.update", "users", id, before, after)
	n, err := s.Store.CountKeysOutsideAccess(r.Context(), "", id)
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, struct {
		*repo.User
		AffectedKeys int `json:"affected_keys"` // see updateGroup
	}{after, n})
}

// disableUser is the DELETE route: users are disabled, never removed, so their
// spend history stays attributable.
func (s *Server) disableUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == auth.UserFrom(r.Context()).ID {
		badRequest(w, "you can't disable yourself")
		return
	}
	t := true
	u, err := s.Store.UpdateUser(r.Context(), id, repo.UserUpdate{Disabled: &t})
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "user.disable", "users", id, nil, u)
	w.WriteHeader(204)
}
