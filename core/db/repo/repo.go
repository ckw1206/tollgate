// Package repo is the only package that talks SQL.
package repo

import (
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
)

type Store struct {
	pool *pgxpool.Pool
}

func New(d *db.DB) *Store { return &Store{pool: d.Pool} }

// Limits is shared by groups and API keys.
type Limits struct {
	BudgetType    string     `json:"budget_type"`
	BudgetNanos   int64      `json:"budget_nanos"`
	SpendNanos    int64      `json:"spend_nanos"`
	BudgetResetAt *time.Time `json:"budget_reset_at"`
	RPMLimit      *int32     `json:"rpm_limit"`
	TPMLimit      *int32     `json:"tpm_limit"`
}

// OverBudget reports whether the current window's spend has reached the cap.
func (l Limits) OverBudget() bool {
	return l.BudgetType != "unlimited" && l.SpendNanos >= l.BudgetNanos
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
