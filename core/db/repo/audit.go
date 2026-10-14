package repo

import (
	"context"
	"encoding/json"
	"time"
)

type AuditEntry struct {
	ID         int64           `json:"id"`
	ActorID    *string         `json:"actor_id"`
	ActorEmail string          `json:"actor_email"`
	Action     string          `json:"action"`
	Resource   string          `json:"resource"`
	ResourceID string          `json:"resource_id"`
	IP         string          `json:"ip"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

type AuditFilter struct {
	ActorID, Action string
	From, To        *time.Time
	Limit, Offset   int
}

func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	if len(e.Details) == 0 {
		e.Details = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_email, action, resource, resource_id, ip, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ActorID, e.ActorEmail, e.Action, e.Resource, e.ResourceID, e.IP, e.Details)
	return err
}

// ListAudit returns newest first plus the total count matching the filter.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, int, error) {
	// Limit defaults to 50 and is capped at 200; a negative offset reads from the start.
	if f.Limit <= 0 {
		f.Limit = 50
	}
	f.Limit, f.Offset = min(f.Limit, 200), max(f.Offset, 0)
	where := `WHERE ($1 = '' OR actor_id::text = $1) AND ($2 = '' OR action LIKE $2 || '%')
	            AND ($3::timestamptz IS NULL OR created_at >= $3) AND ($4::timestamptz IS NULL OR created_at < $4)`
	args := []any{f.ActorID, f.Action, f.From, f.To}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_id, actor_email, action, resource, resource_id, ip, details, created_at
		FROM audit_logs `+where+` ORDER BY id DESC LIMIT $5 OFFSET $6`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.Resource, &e.ResourceID, &e.IP, &e.Details, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
