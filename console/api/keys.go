package consoleapi

import (
	"net/http"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
	"tollgate/core/db/repo"
)

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	owner := me.ID
	if rbac.Has(me.Role, rbac.KeysReadAll) {
		owner = r.URL.Query().Get("user_id") // "" = everyone
	}
	ks, err := s.Store.ListKeys(r.Context(), owner)
	if err != nil {
		storeErr(w, err)
		return
	}
	if ks == nil {
		ks = []repo.APIKey{}
	}
	api.WriteJSON(w, 200, ks)
}

func validKey(w http.ResponseWriter, in repo.KeyInput) bool {
	switch {
	case in.Alias == "":
		badRequest(w, "alias is required")
	case !validBudget(in.BudgetType):
		badRequest(w, "budget_type must be unlimited, daily, monthly, or total")
	case in.BudgetNanos < 0:
		badRequest(w, "budget must not be negative")
	default:
		return true
	}
	return false
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in repo.KeyInput
	if !decode(w, r, &in) || !validKey(w, in) {
		return
	}
	me := auth.UserFrom(r.Context())
	// Only admins choose the billing group; everyone else bills to their own.
	if !rbac.Has(me.Role, rbac.KeysWriteAll) || in.GroupID == "" {
		in.GroupID = me.GroupID
	}
	raw, k, err := s.Store.CreateKey(r.Context(), me.ID, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "key.create", "api_keys", k.ID, nil, k)
	api.WriteJSON(w, 201, map[string]any{"key": k, "raw": raw})
}

// ownedKey loads a key the caller may modify. Keys owned by someone else are
// reported as 404, not 403, so IDs can't be probed.
func (s *Server) ownedKey(w http.ResponseWriter, r *http.Request) (*repo.APIKey, bool) {
	k, err := s.Store.GetKey(r.Context(), r.PathValue("id"))
	me := auth.UserFrom(r.Context())
	if err != nil || (k.UserID != me.ID && !rbac.Has(me.Role, rbac.KeysWriteAll)) {
		notFound(w)
		return nil, false
	}
	return k, true
}

func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	before, ok := s.ownedKey(w, r)
	if !ok {
		return
	}
	var in repo.KeyInput
	if !decode(w, r, &in) || !validKey(w, in) {
		return
	}
	if !rbac.Has(auth.UserFrom(r.Context()).Role, rbac.KeysWriteAll) || in.GroupID == "" {
		in.GroupID = before.GroupID
	}
	after, err := s.Store.UpdateKey(r.Context(), before.ID, in)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "key.update", "api_keys", before.ID, before, after)
	api.WriteJSON(w, 200, after)
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	k, ok := s.ownedKey(w, r)
	if !ok {
		return
	}
	if err := s.Store.RevokeKey(r.Context(), k.ID); err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "key.revoke", "api_keys", k.ID, k, nil)
	w.WriteHeader(204)
}
