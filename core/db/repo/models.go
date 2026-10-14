package repo

import (
	"context"
	"encoding/json"
	"time"
)

type ModelInput struct {
	Name                  string          `json:"name"`
	Provider              string          `json:"provider"`
	UpstreamModel         string          `json:"upstream_model"`
	APIBase               string          `json:"api_base"`
	CredentialID          *string         `json:"credential_id"`
	Enabled               bool            `json:"enabled"`
	PriceInputMicros      int64           `json:"price_input_micros"`
	PriceOutputMicros     int64           `json:"price_output_micros"`
	PriceCacheReadMicros  *int64          `json:"price_cache_read_micros"`
	PriceCacheWriteMicros *int64          `json:"price_cache_write_micros"`
	ContextWindow         *int64          `json:"context_window"`
	Extra                 json.RawMessage `json:"extra"`
}

type Model struct {
	ID string `json:"id"`
	ModelInput
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const modelCols = `id, name, provider, upstream_model, api_base, credential_id, enabled,
	price_input_micros, price_output_micros, price_cache_read_micros, price_cache_write_micros, context_window, extra, created_at, updated_at`

func scanModel(r scanner) (*Model, error) {
	var m Model
	err := r.Scan(&m.ID, &m.Name, &m.Provider, &m.UpstreamModel, &m.APIBase, &m.CredentialID, &m.Enabled,
		&m.PriceInputMicros, &m.PriceOutputMicros, &m.PriceCacheReadMicros, &m.PriceCacheWriteMicros, &m.ContextWindow, &m.Extra, &m.CreatedAt, &m.UpdatedAt)
	return &m, notFound(err)
}

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+modelCols+` FROM models ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func (s *Store) GetModel(ctx context.Context, id string) (*Model, error) {
	return scanModel(s.pool.QueryRow(ctx, `SELECT `+modelCols+` FROM models WHERE id=$1`, id))
}

func extraOrEmpty(e json.RawMessage) json.RawMessage {
	if len(e) == 0 {
		return json.RawMessage(`{}`)
	}
	return e
}

func (s *Store) CreateModel(ctx context.Context, in ModelInput) (*Model, error) {
	m, err := scanModel(s.pool.QueryRow(ctx, `
		INSERT INTO models (name, provider, upstream_model, api_base, credential_id, enabled,
			price_input_micros, price_output_micros, price_cache_read_micros, price_cache_write_micros, context_window, extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+modelCols,
		in.Name, in.Provider, in.UpstreamModel, in.APIBase, in.CredentialID, in.Enabled,
		in.PriceInputMicros, in.PriceOutputMicros, in.PriceCacheReadMicros, in.PriceCacheWriteMicros, in.ContextWindow, extraOrEmpty(in.Extra)))
	return m, conflict(err)
}

func (s *Store) UpdateModel(ctx context.Context, id string, in ModelInput) (*Model, error) {
	m, err := scanModel(s.pool.QueryRow(ctx, `
		UPDATE models SET name=$2, provider=$3, upstream_model=$4, api_base=$5, credential_id=$6, enabled=$7,
			price_input_micros=$8, price_output_micros=$9, price_cache_read_micros=$10, price_cache_write_micros=$11,
			context_window=$12, extra=$13, updated_at=now()
		WHERE id=$1 RETURNING `+modelCols,
		id, in.Name, in.Provider, in.UpstreamModel, in.APIBase, in.CredentialID, in.Enabled,
		in.PriceInputMicros, in.PriceOutputMicros, in.PriceCacheReadMicros, in.PriceCacheWriteMicros, in.ContextWindow, extraOrEmpty(in.Extra)))
	return m, conflict(err)
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM models WHERE id=$1`, id)
	if fkViolation(err) {
		return ErrConflict
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
