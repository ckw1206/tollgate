package repo

import (
	"context"
	"strings"
	"time"
)

type Invite struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	GroupID   string     `json:"group_id"`
	Email     string     `json:"email"`
	CreatedBy string     `json:"created_by"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedBy    *string    `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func (i *Invite) Status() string {
	switch {
	case i.UsedAt != nil:
		return "used"
	case i.RevokedAt != nil:
		return "revoked"
	case time.Now().After(i.ExpiresAt):
		return "expired"
	}
	return "active"
}

const inviteCols = `id, role, group_id, COALESCE(email, ''), created_by, expires_at, used_by, used_at, revoked_at, created_at`

func scanInvite(r scanner) (*Invite, error) {
	var i Invite
	err := r.Scan(&i.ID, &i.Role, &i.GroupID, &i.Email, &i.CreatedBy, &i.ExpiresAt, &i.UsedBy, &i.UsedAt, &i.RevokedAt, &i.CreatedAt)
	return &i, notFound(err)
}

// CreateInvite returns the raw token (for the link) once; only its hash is stored.
func (s *Store) CreateInvite(ctx context.Context, createdBy, role, groupID, email string, ttl time.Duration) (string, *Invite, error) {
	token := RandomToken(32)
	var emailArg any
	if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
		emailArg = email
	}
	inv, err := scanInvite(s.pool.QueryRow(ctx, `
		INSERT INTO invites (token_hash, role, group_id, email, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + make_interval(secs => $6)) RETURNING `+inviteCols,
		HashToken(token), role, groupID, emailArg, createdBy, ttl.Seconds()))
	return token, inv, err
}

func (s *Store) GetInviteByToken(ctx context.Context, token string) (*Invite, error) {
	return scanInvite(s.pool.QueryRow(ctx, `SELECT `+inviteCols+` FROM invites WHERE token_hash=$1`, HashToken(token)))
}

func (s *Store) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+inviteCols+` FROM invites ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE invites SET revoked_at=now() WHERE id=$1 AND used_at IS NULL AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AcceptInvite claims the invite and creates the user in one transaction.
// The conditional UPDATE row-locks the invite, so concurrent accepts serialize
// and only the first sees a usable row.
func (s *Store) AcceptInvite(ctx context.Context, token, email, name, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id, role, groupID string
	err = tx.QueryRow(ctx, `
		UPDATE invites SET used_at = now()
		WHERE token_hash=$1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		  AND (email IS NULL OR email = $2)
		RETURNING id, role, group_id`, HashToken(token), email).Scan(&id, &role, &groupID)
	if notFound(err) == ErrNotFound {
		return nil, ErrInviteUnusable
	} else if err != nil {
		return nil, err
	}
	u, err := createUser(ctx, tx, email, name, password, role, groupID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET used_by=$2 WHERE id=$1`, id, u.ID); err != nil {
		return nil, err
	}
	return u, tx.Commit(ctx)
}
