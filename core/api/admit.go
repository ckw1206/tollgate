package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

type scope struct {
	name, id string
	limits   repo.Limits
}

func scopes(info *repo.AuthInfo) []scope {
	return []scope{{"key", info.Key.ID, info.Key.Limits}, {"group", info.Group.ID, info.Group.Limits}}
}

// modelAllowed: the owner may use it (group or extra grant), and the key narrows from there.
// Unknown or disabled models never get here; they are 404 model_not_found.
func modelAllowed(info *repo.AuthInfo, model string) bool {
	return repo.Access(info.GroupModels, info.ExtraModels, model) != "" &&
		(len(info.Key.Models) == 0 || slices.Contains(info.Key.Models, model))
}

// admit enforces budgets, then RPM, then TPM, for both key and group.
// Redis errors fail closed: an unenforceable limit must not become no limit.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, info *repo.AuthInfo) bool {
	for _, sc := range scopes(info) {
		if sc.limits.OverBudget() {
			WriteError(w, 429, "insufficient_quota", "budget_exceeded", sc.name+" budget exceeded")
			return false
		}
	}
	now := s.Now()
	retry := strconv.FormatInt(60-now.Unix()%60, 10)
	for _, sc := range scopes(info) {
		if sc.limits.RPMLimit != nil {
			n, err := s.Cache.HitRPM(r.Context(), sc.name, sc.id, now)
			if err != nil {
				slog.Error("rate limiter unavailable", "err", err)
				WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
				return false
			}
			if n > int64(*sc.limits.RPMLimit) {
				w.Header().Set("Retry-After", retry)
				WriteError(w, 429, "rate_limit_error", "rate_limit_exceeded", fmt.Sprintf("%s RPM limit %d exceeded", sc.name, *sc.limits.RPMLimit))
				return false
			}
		}
		if sc.limits.TPMLimit != nil {
			n, err := s.Cache.TPM(r.Context(), sc.name, sc.id, now)
			if err != nil {
				slog.Error("rate limiter unavailable", "err", err)
				WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
				return false
			}
			if n >= int64(*sc.limits.TPMLimit) {
				w.Header().Set("Retry-After", retry)
				WriteError(w, 429, "rate_limit_error", "rate_limit_exceeded", fmt.Sprintf("%s TPM limit %d exceeded", sc.name, *sc.limits.TPMLimit))
				return false
			}
		}
	}
	return true
}

// finish records spend and charges TPM. Called once per admitted request that reached upstream.
func (s *Server) finish(info *repo.AuthInfo, rec repo.SpendRecord, u llm.Usage) {
	s.Record(rec)
	if u.Tokens() == 0 {
		if rec.Status == "success" {
			// A provider that stops reporting usage would otherwise bill $0 silently.
			slog.Warn("upstream reported no token usage; request billed $0", "model", rec.ModelName, "provider", rec.Provider)
		}
		return
	}
	now := s.Now()
	for _, sc := range scopes(info) {
		if sc.limits.TPMLimit != nil {
			if err := s.Cache.AddTPM(context.Background(), sc.name, sc.id, u.Tokens(), now); err != nil {
				slog.Error("add TPM", "err", err)
			}
		}
	}
}
