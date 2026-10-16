package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	GroupID      string    `json:"group_id"`
	Disabled     bool      `json:"disabled"`
	ExtraModels  []string  `json:"extra_models"` // granted on top of the group's; empty = none
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UserUpdate struct {
	Name        *string   `json:"name"`
	Role        *string   `json:"role"`
	GroupID     *string   `json:"group_id"`
	Disabled    *bool     `json:"disabled"`
	ExtraModels *[]string `json:"extra_models"`
	Password    *string   `json:"password"` // absent or empty string = no change; non-empty is bcrypt(12)ed here, by the data layer
}

const userCols = `id, email, name, password_hash, role, group_id, disabled, extra_models, created_at, updated_at`

func scanUser(r scanner) (*User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.GroupID, &u.Disabled, &u.ExtraModels, &u.CreatedAt, &u.UpdatedAt)
	return &u, notFound(err)
}

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(h), err
}

func CheckPassword(u *User, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(pw)) == nil
}

func (s *Store) CreateUser(ctx context.Context, email, name, password, role, groupID string) (*User, error) {
	return createUser(ctx, s.pool, email, name, password, role, groupID)
}

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx (AcceptInvite uses the latter).
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func createUser(ctx context.Context, q rowQuerier, email, name, password, role, groupID string) (*User, error) {
	h, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	u, err := scanUser(q.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, group_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+userCols,
		strings.ToLower(strings.TrimSpace(email)), name, h, role, groupID))
	return u, conflict(err)
}

func (s *Store) CountUsers(ctx context.Context) (n int, err error) {
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// bootstrapLock is the pg_advisory_xact_lock key that serializes first-run setups.
const bootstrapLock int64 = 0x746f6c6c67617465 // "tollgate"

// BootstrapAdmin creates the first user, an admin in the default group, only while
// the users table is empty; otherwise it returns ErrAlreadySetUp. The advisory lock
// is needed because under READ COMMITTED two concurrent INSERT … WHERE NOT EXISTS
// can both see an empty table; with it, the second runs after the first commits.
func (s *Store) BootstrapAdmin(ctx context.Context, email, name, password string) (*User, error) {
	h, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapLock); err != nil {
		return nil, err
	}
	u, err := scanUser(tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role, group_id)
		SELECT $1::text, $2::text, $3::text, 'admin'::user_role, g.id FROM groups g
		WHERE g.name = 'default' AND NOT EXISTS (SELECT 1 FROM users)
		RETURNING `+userCols,
		strings.ToLower(strings.TrimSpace(email)), name, h))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrAlreadySetUp
	} else if err != nil {
		return nil, conflict(err)
	}
	return u, tx.Commit(ctx)
}

func (s *Store) GetUser(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id string, in UserUpdate) (*User, error) {
	var pw *string
	if in.Password != nil && *in.Password != "" {
		h, err := hashPassword(*in.Password)
		if err != nil {
			return nil, err
		}
		pw = &h
	}
	return scanUser(s.pool.QueryRow(ctx, `
		UPDATE users SET
			name     = COALESCE($2, name),
			role     = COALESCE($3::user_role, role),
			group_id = COALESCE($4::uuid, group_id),
			disabled = COALESCE($5, disabled),
			extra_models = COALESCE($6::text[], extra_models),
			password_hash = COALESCE($7, password_hash),
			updated_at = now()
		WHERE id=$1 RETURNING `+userCols, id, in.Name, in.Role, in.GroupID, in.Disabled, in.ExtraModels, pw))
}
