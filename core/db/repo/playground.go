package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaxPlaygroundSessions caps saved sessions per user; updates never count.
const MaxPlaygroundSessions = 50

var ErrSessionLimit = errors.New("playground session limit reached")

// PlaygroundSession is a saved playground conversation, private to its owner.
// Every query is scoped by user_id, so another user's session is indistinguishable
// from a missing one (ErrNotFound) — no cross-user reads or writes.
type PlaygroundSession struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Name      string          `json:"name"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// ListPlaygroundSessions returns the user's sessions, newest first. Only ID,
// Name and UpdatedAt are populated (Data and CreatedAt stay zero); the console
// list only needs those, and the full row loads lazily via GetPlaygroundSession.
func (s *Store) ListPlaygroundSessions(ctx context.Context, userID string) ([]PlaygroundSession, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, user_id, name, updated_at FROM playground_sessions WHERE user_id = $1
		 ORDER BY updated_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlaygroundSession
	for rows.Next() {
		var p PlaygroundSession
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPlaygroundSession(ctx context.Context, userID, id string) (*PlaygroundSession, error) {
	var p PlaygroundSession
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, name, data, created_at, updated_at FROM playground_sessions WHERE id = $2 AND user_id = $1`,
		userID, id).Scan(&p.ID, &p.UserID, &p.Name, &p.Data, &p.CreatedAt, &p.UpdatedAt)
	return &p, notFound(err)
}

func (s *Store) CreatePlaygroundSession(ctx context.Context, userID, name string, data json.RawMessage) (*PlaygroundSession, error) {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	// ponytail: count-then-insert in one statement; two creates racing at 49
	// can both land (51). Harmless for a soft cap — lock the user row if it must be exact.
	var p PlaygroundSession
	err := s.pool.QueryRow(ctx,
		`INSERT INTO playground_sessions (user_id, name, data)
		 SELECT $1, $2, $3 WHERE (SELECT count(*) FROM playground_sessions WHERE user_id = $1) < $4
		 RETURNING id, user_id, name, data, created_at, updated_at`,
		userID, name, data, MaxPlaygroundSessions).Scan(&p.ID, &p.UserID, &p.Name, &p.Data, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionLimit
	}
	return &p, err
}

func (s *Store) UpdatePlaygroundSession(ctx context.Context, userID, id, name string, data json.RawMessage) (*PlaygroundSession, error) {
	if data == nil {
		data = json.RawMessage(`{}`)
	}
	var p PlaygroundSession
	err := s.pool.QueryRow(ctx,
		`UPDATE playground_sessions SET name = $3, data = $4, updated_at = now()
		 WHERE id = $2 AND user_id = $1
		 RETURNING id, user_id, name, data, created_at, updated_at`,
		userID, id, name, data).Scan(&p.ID, &p.UserID, &p.Name, &p.Data, &p.CreatedAt, &p.UpdatedAt)
	return &p, notFound(err)
}

func (s *Store) DeletePlaygroundSession(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM playground_sessions WHERE id = $2 AND user_id = $1`, userID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
