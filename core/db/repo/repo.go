// Package repo is the only package that talks SQL.
package repo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tollgate/core/db"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("already exists")
	ErrInviteUnusable = errors.New("invite is expired, revoked, used, or bound to another email")
	ErrAlreadySetUp   = errors.New("an account already exists")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(d *db.DB) *Store { return &Store{pool: d.Pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Limits is shared by groups and API keys.
type Limits struct {
	BudgetType    string     `json:"budget_type"`
	BudgetNanos   int64      `json:"budget_nanos"`
	SpendNanos    int64      `json:"spend_nanos"`
	BudgetResetAt *time.Time `json:"budget_reset_at"`
	RPMLimit      *int32     `json:"rpm_limit"`
	TPMLimit      *int32     `json:"tpm_limit"`
}

// EffectiveSpend reports the spend that counts toward the cap as of now.
// A daily/monthly window whose budget_reset_at is already in the past has not
// been reset by the worker yet, so its spend must be treated as fresh (0) —
// otherwise the request is blocked until midnight rolls over and the reset
// runs (#21).
func (l Limits) EffectiveSpend(now time.Time) int64 {
	if l.isWindowed() && l.BudgetResetAt != nil && !now.Before(*l.BudgetResetAt) {
		return 0
	}
	return l.SpendNanos
}

// WindowStart reports the start of the window a record should be charged to,
// or nil for total/unlimited budgets and when there is no window (nil
// BudgetResetAt). An expired daily/monthly window starts at the reset instant
// itself; an active one starts one period before the reset.
func (l Limits) WindowStart(now time.Time) *time.Time {
	if !l.isWindowed() || l.BudgetResetAt == nil {
		return nil
	}
	start := *l.BudgetResetAt
	if !now.Before(start) {
		return l.BudgetResetAt // expired: the new window began at the reset instant
	}
	if l.BudgetType == "monthly" {
		start = start.AddDate(0, -1, 0)
	} else {
		start = start.AddDate(0, 0, -1)
	}
	return &start
}

// OverBudget reports whether the current window's spend has reached the cap.
func (l Limits) OverBudget(now time.Time) bool {
	return l.BudgetType != "unlimited" && l.EffectiveSpend(now) >= l.BudgetNanos
}

func (l Limits) isWindowed() bool {
	return l.BudgetType == "daily" || l.BudgetType == "monthly"
}

// BudgetCrossing reports that a key or group's effective spend crossed a
// threshold (80% or 100% of its budget cap) within the current window. It is
// fired at most once per scope per threshold per window (enforced by the
// one-shot notify marker). Label is the key alias or group name — never a raw
// key, hash, or prefix.
type BudgetCrossing struct {
	Scope         string     `json:"scope"` // "key" or "group"
	ID            string     `json:"id"`
	Label         string     `json:"label"`       // key alias or group name (never a raw key/hash/prefix)
	Threshold     int        `json:"threshold"`   // 80 or 100
	SpendNanos    int64      `json:"spend_nanos"` // effective spend after this batch
	BudgetNanos   int64      `json:"budget_nanos"`
	BudgetType    string     `json:"budget_type"`
	BudgetResetAt *time.Time `json:"budget_reset_at"` // the current window's reset instant
}

func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func conflict(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return ErrConflict
	}
	return err
}
