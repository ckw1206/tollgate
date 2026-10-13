package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type Credential struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Ciphertext []byte    `json:"-"`
	Nonce      []byte    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

const credCols = `id, name, kind, ciphertext, nonce, created_at`

func scanCred(r scanner) (*Credential, error) {
	var c Credential
	err := r.Scan(&c.ID, &c.Name, &c.Kind, &c.Ciphertext, &c.Nonce, &c.CreatedAt)
	return &c, notFound(err)
}

func (s *Store) CreateCredential(ctx context.Context, name, kind string, nonce, ct []byte) (*Credential, error) {
	c, err := scanCred(s.pool.QueryRow(ctx, `
		INSERT INTO provider_credentials (name, kind, nonce, ciphertext) VALUES ($1, $2, $3, $4) RETURNING `+credCols,
		name, kind, nonce, ct))
	return c, conflict(err)
}

func (s *Store) GetCredential(ctx context.Context, id string) (*Credential, error) {
	return scanCred(s.pool.QueryRow(ctx, `SELECT `+credCols+` FROM provider_credentials WHERE id=$1`, id))
}

func (s *Store) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credCols+` FROM provider_credentials ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredential(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM provider_credentials WHERE id=$1`, id)
	if fkViolation(err) {
		return ErrConflict
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func fkViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}
