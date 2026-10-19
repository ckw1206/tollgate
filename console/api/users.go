package consoleapi

import (
	"errors"
	"net/http"
	"slices"
	"strings"

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

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		GroupID  string `json:"group_id"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Email) == "" {
		badRequest(w, "email is required")
		return
	}
	if !rbac.ValidRole(in.Role) {
		badRequest(w, "role must be admin, member, or viewer")
		return
	}
	if len(in.Password) < 12 {
		badRequest(w, "password must be at least 12 characters")
		return
	}
	if _, err := s.Store.GetGroup(r.Context(), in.GroupID); err != nil {
		badRequest(w, "unknown group")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), in.Email, in.Name, in.Password, in.Role, in.GroupID)
	if err != nil {
		if errors.Is(err, repo.ErrConflict) {
			api.WriteError(w, 409, "invalid_request_error", "email_taken", "a user with this email already exists")
			return
		}
		storeErr(w, err)
		return
	}
	s.audit(r, "user.create", "users", u.ID, nil, u)
	api.WriteJSON(w, 201, u)
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
	if in.Password != nil && *in.Password != "" && len(*in.Password) < 12 {
		badRequest(w, "password must be at least 12 characters")
		return
	}
	if in.GroupID != nil {
		if _, err := s.Store.GetGroup(r.Context(), *in.GroupID); err != nil {
			badRequest(w, "unknown group")
			return
		}
	}
	before, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	if !s.knownModels(w, r, in.ExtraModels, before.ExtraModels) {
		return
	}
	// Compare canonical IDs: Postgres also accepts the path id uppercase or without dashes.
	self := before.ID == auth.UserFrom(r.Context()).ID
	if self && ((in.Role != nil && *in.Role != "admin") || (in.Disabled != nil && *in.Disabled)) {
		badRequest(w, "you can't demote or disable yourself")
		return
	}
	if self && ((in.GroupID != nil && *in.GroupID != before.GroupID) || (in.ExtraModels != nil && !slices.Equal(*in.ExtraModels, before.ExtraModels))) {
		badRequest(w, "you can't change your own group or model access")
		return
	}
	after, err := s.Store.UpdateUser(r.Context(), id, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "user.update", "users", id, before, after)
	n, err := s.Store.CountKeysOutsideAccess(r.Context(), "", after.ID)
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
	u, err := s.Store.GetUser(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	if u.ID == auth.UserFrom(r.Context()).ID { // canonical, like updateUser
		badRequest(w, "you can't disable yourself")
		return
	}
	t := true
	u, err = s.Store.UpdateUser(r.Context(), id, repo.UserUpdate{Disabled: &t})
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "user.disable", "users", id, nil, u)
	w.WriteHeader(204)
}
