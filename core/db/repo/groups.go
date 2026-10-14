package repo

import (
	"context"
	"slices"
	"time"
)

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Limits
	Models    []string  `json:"models"`  // empty = every enabled model
	Members   int       `json:"members"` // users in the group, disabled included
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GroupInput struct {
	Name        string    `json:"name"`
	BudgetType  string    `json:"budget_type"`
	BudgetNanos int64     `json:"budget_nanos"`
	RPMLimit    *int32    `json:"rpm_limit"`
	TPMLimit    *int32    `json:"tpm_limit"`
	Models      *[]string `json:"models"` // nil keeps the current list; [] = all
}

const groupCols = `id, name, budget_type, budget_nanos, spend_nanos, budget_reset_at, rpm_limit, tpm_limit,
	models, (SELECT count(*) FROM users u WHERE u.group_id = groups.id), created_at, updated_at`

// Access says why a user may call model: "group", "granted", or "" (not allowed).
// An empty groupModels allows everything; callers check that the model is enabled.
func Access(groupModels, extra []string, model string) string {
	switch {
	case len(groupModels) == 0 || slices.Contains(groupModels, model):
		return "group"
	case slices.Contains(extra, model):
		return "granted"
	}
	return ""
}

type scanner interface{ Scan(dest ...any) error }

func scanGroup(r scanner) (*Group, error) {
	var g Group
	err := r.Scan(&g.ID, &g.Name, &g.BudgetType, &g.BudgetNanos, &g.SpendNanos, &g.BudgetResetAt, &g.RPMLimit, &g.TPMLimit, &g.Models, &g.Members, &g.CreatedAt, &g.UpdatedAt)
	return &g, notFound(err)
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+groupCols+` FROM groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (s *Store) GetGroup(ctx context.Context, id string) (*Group, error) {
	return scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupCols+` FROM groups WHERE id=$1`, id))
}

func (s *Store) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	return scanGroup(s.pool.QueryRow(ctx, `SELECT `+groupCols+` FROM groups WHERE name=$1`, name))
}

func (s *Store) CreateGroup(ctx context.Context, in GroupInput) (*Group, error) {
	g, err := scanGroup(s.pool.QueryRow(ctx, `
		INSERT INTO groups (name, budget_type, budget_nanos, budget_reset_at, rpm_limit, tpm_limit, models)
		VALUES ($1, $2, $3, next_budget_reset($2), $4, $5, COALESCE($6::text[], '{}')) RETURNING `+groupCols,
		in.Name, in.BudgetType, in.BudgetNanos, in.RPMLimit, in.TPMLimit, in.Models))
	return g, conflict(err)
}

// UpdateGroup replaces the editable fields, except that a nil Models keeps the list.
// Changing budget_type restarts the window; changing budget_type or budget_nanos
// re-arms the one-shot notify marker (A11).
func (s *Store) UpdateGroup(ctx context.Context, id string, in GroupInput) (*Group, error) {
	g, err := scanGroup(s.pool.QueryRow(ctx, `
		UPDATE groups SET name=$2, budget_nanos=$4, rpm_limit=$5, tpm_limit=$6, models=COALESCE($7::text[], models), updated_at=now(),
			spend_nanos     = CASE WHEN budget_type <> $3::budget_type THEN 0 ELSE spend_nanos END,
			budget_reset_at = CASE WHEN budget_type <> $3::budget_type THEN next_budget_reset($3::budget_type) ELSE budget_reset_at END,
			budget_notified_window = CASE WHEN budget_type <> $3::budget_type OR budget_nanos <> $4 THEN NULL ELSE budget_notified_window END,
			budget_notified_pct    = CASE WHEN budget_type <> $3::budget_type OR budget_nanos <> $4 THEN 0 ELSE budget_notified_pct END,
			budget_type=$3
		WHERE id=$1 RETURNING `+groupCols,
		id, in.Name, in.BudgetType, in.BudgetNanos, in.RPMLimit, in.TPMLimit, in.Models))
	return g, conflict(err)
}
