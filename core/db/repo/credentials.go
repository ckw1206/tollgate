package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// CredentialInput is everything about a credential except its secret.
type CredentialInput struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`     // api_key | gcp_service_account
	Provider       string `json:"provider"` // openai | anthropic | google
	APIBase        string `json:"api_base"` // "" = provider default
	VertexProject  string `json:"vertex_project"`
	VertexLocation string `json:"vertex_location"`
}

type Credential struct {
	ID string `json:"id"`
	CredentialInput
	Ciphertext      []byte    `json:"-"`
	Nonce           []byte    `json:"-"`
	UsedBy          []string  `json:"used_by"` // names of the models using it
	SecretUpdatedAt time.Time `json:"secret_updated_at"`
	CreatedAt       time.Time `json:"created_at"`
}

const credCols = `id, name, kind, provider, api_base, vertex_project, vertex_location, ciphertext, nonce,
	ARRAY(SELECT m.name FROM models m WHERE m.credential_id = provider_credentials.id ORDER BY m.name),
	secret_updated_at, created_at`

func scanCred(r scanner) (*Credential, error) {
	var c Credential
	err := r.Scan(&c.ID, &c.Name, &c.Kind, &c.Provider, &c.APIBase, &c.VertexProject, &c.VertexLocation, &c.Ciphertext, &c.Nonce,
		&c.UsedBy, &c.SecretUpdatedAt, &c.CreatedAt)
	return &c, notFound(err)
}

func (s *Store) CreateCredential(ctx context.Context, in CredentialInput, nonce, ct []byte) (*Credential, error) {
	c, err := scanCred(s.pool.QueryRow(ctx, `
		INSERT INTO provider_credentials (name, kind, provider, api_base, vertex_project, vertex_location, nonce, ciphertext)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+credCols,
		in.Name, in.Kind, in.Provider, in.APIBase, in.VertexProject, in.VertexLocation, nonce, ct))
	return c, conflict(err)
}

// UpdateCredential sets everything but kind and provider. nonce/ct are always the secret
// sealed under the (possibly new) name; secretChanged also bumps secret_updated_at.
func (s *Store) UpdateCredential(ctx context.Context, id string, in CredentialInput, nonce, ct []byte, secretChanged bool) (*Credential, error) {
	c, err := scanCred(s.pool.QueryRow(ctx, `
		UPDATE provider_credentials SET name=$2, api_base=$3, vertex_project=$4, vertex_location=$5, nonce=$6, ciphertext=$7,
			secret_updated_at = CASE WHEN $8 THEN now() ELSE secret_updated_at END
		WHERE id=$1 RETURNING `+credCols,
		id, in.Name, in.APIBase, in.VertexProject, in.VertexLocation, nonce, ct, secretChanged))
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
