package consoleapi

import (
	"net/http"
	"time"

	"tollgate/console/auth"
	"tollgate/console/rbac"
	"tollgate/core/api"
)

// gate: the one-line status bar shared by every console page. The budget is
// the caller's own group (users.group_id) — the same limits the playground is
// metered against — with EffectiveSpend applied so a not-yet-reset window
// reads as fresh, and null when the group is unlimited.
func (s *Server) gate(w http.ResponseWriter, r *http.Request) {
	me := auth.UserFrom(r.Context())
	readAll := rbac.Has(me.Role, rbac.SpendReadAll)
	scope := me.ID
	if readAll {
		scope = "" // spend:read_all sees the fleet-wide median latency
	}
	stats, err := s.Store.GateStats(r.Context(), scope)
	if err != nil {
		storeErr(w, err)
		return
	}
	resp := map[string]any{
		"providers_up":    stats.ProvidersUp,
		"providers_total": stats.ProvidersTotal,
		"rpm":             nil, // fleet request rate is a spend:read_all view
		"p50_ms":          stats.P50MS,
		"budget":          nil,
	}
	if readAll {
		resp["rpm"] = stats.RPM
	}
	g, err := s.Store.GetGroup(r.Context(), me.GroupID)
	if err != nil {
		storeErr(w, err)
		return
	}
	if g.BudgetType != "unlimited" {
		// reserved_nanos is in-flight plus settled-but-uncommitted cost (#23):
		// money already committed against the cap that spend_nanos does not yet
		// show. Displaying spend without it makes the bar understate usage.
		reserved, _, _ := s.Cache.Held(r.Context(), "group", g.ID)
		resp["budget"] = map[string]any{
			"type":           g.BudgetType,
			"budget_nanos":   g.BudgetNanos,
			"spend_nanos":    g.EffectiveSpend(time.Now()),
			"reserved_nanos": reserved,
			"reset_at":       g.BudgetResetAt,
		}
	}
	api.WriteJSON(w, 200, resp)
}
