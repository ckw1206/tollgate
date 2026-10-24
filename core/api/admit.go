package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"tollgate/core/cache"
	"tollgate/core/db/repo"
	"tollgate/core/llm"
)

type scope struct {
	name, id string
	limits   repo.Limits
}

// scopes returns the limits to enforce, tightest first. A key-less
// (playground) principal has no key scope: only its group's budget, RPM and TPM.
func scopes(info *repo.AuthInfo) []scope {
	var out []scope
	if info.Key.ID != "" {
		out = append(out, scope{"key", info.Key.ID, info.Key.Limits})
	}
	return append(out, scope{"group", info.Group.ID, info.Group.Limits})
}

// modelAllowed: the owner may use it (group or extra grant), and the key narrows from there.
// Unknown or disabled models never get here; they are 404 model_not_found.
func modelAllowed(info *repo.AuthInfo, model string) bool {
	return repo.Access(info.GroupModels, info.ExtraModels, model) != "" &&
		(len(info.Key.Models) == 0 || slices.Contains(info.Key.Models, model))
}

// reservation is what admit() took against each scope, to be settled by
// finish() with the request's real usage. Nil when the request was refused, or
// when no scope had a cap (nothing to settle).
type reservation struct {
	scopes []reservedScope
}

type reservedScope struct {
	name, id  string
	costNanos int64
	tokens    int64
	hasBudget bool // the scope has a budget cap, so cost was reserved
	hasTPM    bool // the scope has a TPM limit, so tokens were reserved
}

// admit enforces budgets, then RPM, then TPM, for both key and group.
//
// Budgets and TPM are enforced against committed spend PLUS the estimated cost
// of everything currently in flight (#23, #28): the counters alone are only
// written in finish(), so concurrent requests would otherwise all read the same
// pre-spend value and overshoot together.
//
// estimate maps the request to its reservation; chat and embeddings size
// themselves differently (a chat request can commit to output, an embedding
// cannot).
//
// Redis errors fail closed: an unenforceable limit must not become no limit.
// On refusal, any reservation already taken for earlier scopes is released.
func (s *Server) admit(w http.ResponseWriter, r *http.Request, info *repo.AuthInfo,
	estimate func() (int64, int64)) (*reservation, bool) {
	now := s.Now()
	estCost, estTokens := estimate()
	res := &reservation{}

	// release undoes everything reserved so far; used on any refusal path.
	// It runs on a detached context: releasing must never depend on the client
	// still being connected, and a cancelled r.Context() would silently turn a
	// refusal into a leaked reservation.
	release := func() {
		res.release(s.Cache, context.Background())
	}

	for _, sc := range scopes(info) {
		// Budget: committed spend + in-flight reservations + this estimate.
		if sc.limits.BudgetType != "" && sc.limits.BudgetType != "unlimited" {
			cost, _, err := s.Cache.Reserve(r.Context(), sc.name, sc.id, estCost, 0)
			if err != nil {
				slog.Error("reservation unavailable", "err", err)
				release()
				WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
				return nil, false
			}
			res.scopes = append(res.scopes, reservedScope{name: sc.name, id: sc.id, costNanos: estCost, hasBudget: true})
			// EffectiveSpend treats an expired-but-unreset window as 0; the
			// reservation is scoped to the same window for the same reason.
			if sc.limits.EffectiveSpend(now)+cost > sc.limits.BudgetNanos {
				release()
				WriteError(w, 429, "insufficient_quota", "budget_exceeded", sc.name+" budget exceeded")
				return nil, false
			}
		}
		// TPM: committed tokens (this minute) + in-flight reservations.
		if sc.limits.TPMLimit != nil {
			_, tpm, err := s.Cache.Reserve(r.Context(), sc.name, sc.id, 0, estTokens)
			if err != nil {
				slog.Error("reservation unavailable", "err", err)
				release()
				WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
				return nil, false
			}
			res.scopes = append(res.scopes, reservedScope{name: sc.name, id: sc.id, tokens: estTokens, hasTPM: true})
			// TPM has no settled-but-uncommitted hold: tokens are charged by
			// AddTPM in finish() on the same path that settles, so the
			// in-flight reservation is the whole story.
			committed, err := s.Cache.TPM(r.Context(), sc.name, sc.id, now)
			if err != nil {
				slog.Error("rate limiter unavailable", "err", err)
				release()
				WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
				return nil, false
			}
			if committed+tpm >= int64(*sc.limits.TPMLimit) {
				release()
				retry := strconv.FormatInt(60-now.Unix()%60, 10)
				w.Header().Set("Retry-After", retry)
				WriteError(w, 429, "rate_limit_error", "rate_limit_exceeded", fmt.Sprintf("%s TPM limit %d exceeded", sc.name, *sc.limits.TPMLimit))
				return nil, false
			}
		}
	}

	// RPM is a pure counter: HitRPM already increments atomically before the
	// comparison, so it has no in-flight gap to close.
	retry := strconv.FormatInt(60-now.Unix()%60, 10)
	for _, sc := range scopes(info) {
		if sc.limits.RPMLimit == nil {
			continue
		}
		n, err := s.Cache.HitRPM(r.Context(), sc.name, sc.id, now)
		if err != nil {
			slog.Error("rate limiter unavailable", "err", err)
			release()
			WriteError(w, 503, "api_error", "rate_limiter_unavailable", "rate limiter unavailable")
			return nil, false
		}
		if n > int64(*sc.limits.RPMLimit) {
			release()
			w.Header().Set("Retry-After", retry)
			WriteError(w, 429, "rate_limit_error", "rate_limit_exceeded", fmt.Sprintf("%s RPM limit %d exceeded", sc.name, *sc.limits.RPMLimit))
			return nil, false
		}
	}
	return res, true
}

// release drops every reservation this handle holds. Safe to call more than
// once and on a nil handle, so callers can defer it and still settle later.
func (r *reservation) release(c *cache.Cache, ctx context.Context) {
	if r == nil {
		return
	}
	for _, sc := range r.scopes {
		var cost, tokens int64
		if sc.hasBudget {
			cost = sc.costNanos
		}
		if sc.hasTPM {
			tokens = sc.tokens
		}
		if cost == 0 && tokens == 0 {
			continue
		}
		if err := c.Release(ctx, sc.name, sc.id, cost, tokens); err != nil {
			slog.Error("release reservation", "scope", sc.name, "err", err)
		}
	}
	// Any later settle would be a no-op on the released estimate; clear so a
	// double settle can't subtract the estimate twice.
	r.scopes = nil
}

// settle swaps each scope's estimated reservation for the request's real usage.
// Called once per admitted request that reached upstream, from finish().
func (r *reservation) settle(c *cache.Cache, u llm.Usage, actualCost int64) {
	if r == nil {
		return
	}
	ctx := context.Background()
	for _, sc := range r.scopes {
		var rc, rt, ac, at int64
		if sc.hasBudget {
			rc, ac = sc.costNanos, actualCost
		}
		if sc.hasTPM {
			rt, at = sc.tokens, u.Tokens()
		}
		if rc == 0 && rt == 0 && ac == 0 && at == 0 {
			continue
		}
		if err := c.Settle(ctx, sc.name, sc.id, rc, rt, ac, at); err != nil {
			slog.Error("settle reservation", "scope", sc.name, "err", err)
		}
	}
	r.scopes = nil
}

// finish records spend and charges TPM. Called once per admitted request that reached upstream.
// res is the reservation taken by admit; it is settled with the real usage so
// the scope stops counting the estimate and counts the actual cost instead.
func (s *Server) finish(info *repo.AuthInfo, res *reservation, rec repo.SpendRecord, u llm.Usage) {
	s.Record(rec)
	cost := rec.CostNanos
	res.settle(s.Cache, u, cost)
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
