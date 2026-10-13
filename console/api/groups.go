package consoleapi

import (
	"net/http"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	if !rbac.Has(me.Role, rbac.GroupsReadAll) {
		g, err := s.Store.GetGroup(r.Context(), me.GroupID)
		if err != nil {
			storeErr(w, err)
			return
		}
		api.WriteJSON(w, 200, []repo.Group{*g})
		return
	}
	gs, err := s.Store.ListGroups(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, gs)
}

func validGroup(w http.ResponseWriter, in repo.GroupInput) bool {
	switch {
	case in.Name == "":
		badRequest(w, "name is required")
	case !validBudget(in.BudgetType):
		badRequest(w, "budget_type must be unlimited, daily, monthly, or total")
	case in.BudgetNanos < 0:
		badRequest(w, "budget must not be negative")
	default:
		return true
	}
	return false
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in repo.GroupInput
	if !decode(w, r, &in) || !validGroup(w, in) {
		return
	}
	g, err := s.Store.CreateGroup(r.Context(), in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "group.create", "groups", g.ID, nil, g)
	api.WriteJSON(w, 201, g)
}

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in repo.GroupInput
	if !decode(w, r, &in) || !validGroup(w, in) {
		return
	}
	before, err := s.Store.GetGroup(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	after, err := s.Store.UpdateGroup(r.Context(), id, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "group.update", "groups", id, before, after)
	api.WriteJSON(w, 200, after)
}
