package consoleapi

import (
	"errors"
	"net/http"

	"tollgate/core/api"
	"tollgate/core/db/repo"
)

// globalBudgetView is the server-wide cap (#31) as the console sees it: the
// same budget fields a key or group returns, plus the in-flight reservation,
// so the Settings page can show what the gateway enforces. It has no RPM/TPM.
type globalBudgetView struct {
	BudgetType    string `json:"budget_type"`
	BudgetNanos   int64  `json:"budget_nanos"`
	SpendNanos    int64  `json:"spend_nanos"`
	BudgetResetAt any    `json:"budget_reset_at"`
	ReservedNanos int64  `json:"reserved_nanos"`
}

func (s *Server) globalBudgetView(r *http.Request, l repo.Limits) globalBudgetView {
	v := globalBudgetView{BudgetType: l.BudgetType, BudgetNanos: l.BudgetNanos, SpendNanos: l.SpendNanos, BudgetResetAt: l.BudgetResetAt}
	if l.BudgetType != "unlimited" {
		v.ReservedNanos, _, _ = s.Cache.Held(r.Context(), "global", repo.GlobalID) // display only; 0 if Redis is down
	}
	return v
}

// GET /settings/budget (settings:read). Kept off /settings and /auth/me: the
// settings row goes to every role through /auth/me, and global spend is admin data.
func (s *Server) getGlobalBudget(w http.ResponseWriter, r *http.Request) {
	l, err := s.Store.GlobalBudget(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	api.WriteJSON(w, 200, s.globalBudgetView(r, l))
}

// PUT /settings/budget (settings:write): {budget_type, budget_nanos}. A
// separate endpoint from PUT /settings, so an older client that saves the
// settings form without these fields can't switch the cap off by omission.
func (s *Server) putGlobalBudget(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BudgetType  string `json:"budget_type"`
		BudgetNanos int64  `json:"budget_nanos"`
	}
	if !decode(w, r, &in) {
		return
	}
	before, after, err := s.Store.SetGlobalBudget(r.Context(), in.BudgetType, in.BudgetNanos)
	if errors.Is(err, repo.ErrGlobalBudgetType) || errors.Is(err, repo.ErrGlobalBudgetAmount) {
		badRequest(w, err.Error())
		return
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	s.audit(r, "settings.budget", "server_settings", repo.GlobalID, before, after)
	api.WriteJSON(w, 200, s.globalBudgetView(r, after))
}
