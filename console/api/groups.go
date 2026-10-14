package consoleapi

import (
	"fmt"
	"net/http"
	"slices"

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

// knownModels dedupes a model access list in place and rejects names that aren't
// in the models table. Disabled models count: they can be granted ahead of time.
// Names in kept (the stored list) pass too: a model renamed or deleted since stays
// on the list until someone removes it, and doesn't block saving anything else.
func (s *Server) knownModels(w http.ResponseWriter, r *http.Request, names *[]string, kept []string) bool {
	if names == nil {
		return true
	}
	ms, err := s.Store.ListModels(r.Context())
	if err != nil {
		storeErr(w, err)
		return false
	}
	out := []string{}
	for _, n := range *names {
		if !slices.Contains(kept, n) && !slices.ContainsFunc(ms, func(m repo.Model) bool { return m.Name == n }) {
			badRequest(w, fmt.Sprintf("unknown model %q", n))
			return false
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	*names = out
	return true
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var in repo.GroupInput
	if !decode(w, r, &in) || !validGroup(w, in) || !s.knownModels(w, r, in.Models, nil) {
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
	if !s.knownModels(w, r, in.Models, before.Models) {
		return
	}
	after, err := s.Store.UpdateGroup(r.Context(), id, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "group.update", "groups", id, before, after)
	n, err := s.Store.CountKeysOutsideAccess(r.Context(), after.ID, "") // canonical: the path id may be uppercase
	if err != nil {
		storeErr(w, err)
		return
	}
	// affected_keys: live keys whose list now holds a model their owner can't use.
	// Their lists aren't changed; the gateway refuses those models.
	api.WriteJSON(w, 200, struct {
		*repo.Group
		AffectedKeys int `json:"affected_keys"`
	}{after, n})
}
