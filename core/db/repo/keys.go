package repo

import (
	"context"
	"errors"
	"time"
)

type APIKey struct {
	ID        string   `json:"id"`
	KeyPrefix string   `json:"key_prefix"`
	Alias     string   `json:"alias"`
	UserID    string   `json:"user_id"`
	GroupID   string   `json:"group_id"`
	Models    []string `json:"models"`
	Limits
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	RotatedAt  *time.Time `json:"rotated_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type KeyInput struct {
	Alias       string     `json:"alias"`
	GroupID     string     `json:"group_id"`
	Models      []string   `json:"models"`
	BudgetType  string     `json:"budget_type"`
	BudgetNanos int64      `json:"budget_nanos"`
	RPMLimit    *int32     `json:"rpm_limit"`
	TPMLimit    *int32     `json:"tpm_limit"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type AuthInfo struct {
	Key          APIKey
	Group        Group // the key's billing group: budget, RPM, TPM
	UserEmail    string
	UserDisabled bool
	GroupModels  []string // model access comes from the owner's group (users.group_id)
	ExtraModels  []string
}

func GenerateKey() (raw, hash, prefix string) {
	raw = "tg-" + RandomToken(32)
	return raw, HashToken(raw), raw[:12]
}

const keyCols = `k.id, k.key_prefix, k.alias, k.user_id, k.group_id, k.models,
	k.budget_type, k.budget_nanos, k.spend_nanos, k.budget_reset_at, k.rpm_limit, k.tpm_limit,
	k.expires_at, k.revoked_at, k.rotated_at, k.last_used_at, k.created_at`

func keyDest(k *APIKey) []any {
	return []any{&k.ID, &k.KeyPrefix, &k.Alias, &k.UserID, &k.GroupID, &k.Models,
		&k.BudgetType, &k.BudgetNanos, &k.SpendNanos, &k.BudgetResetAt, &k.RPMLimit, &k.TPMLimit,
		&k.ExpiresAt, &k.RevokedAt, &k.RotatedAt, &k.LastUsedAt, &k.CreatedAt}
}

func scanKey(r scanner) (*APIKey, error) {
	var k APIKey
	err := r.Scan(keyDest(&k)...)
	return &k, notFound(err)
}

func nonNil(m []string) []string {
	if m == nil {
		return []string{}
	}
	return m
}

func (s *Store) CreateKey(ctx context.Context, userID string, in KeyInput) (string, *APIKey, error) {
	raw, hash, prefix := GenerateKey()
	k, err := scanKey(s.pool.QueryRow(ctx, `
		INSERT INTO api_keys AS k (key_hash, key_prefix, alias, user_id, group_id, models,
			budget_type, budget_nanos, budget_reset_at, rpm_limit, tpm_limit, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,next_budget_reset($7),$9,$10,$11) RETURNING `+keyCols,
		hash, prefix, in.Alias, userID, in.GroupID, nonNil(in.Models),
		in.BudgetType, in.BudgetNanos, in.RPMLimit, in.TPMLimit, in.ExpiresAt))
	return raw, k, err
}

func (s *Store) GetKey(ctx context.Context, id string) (*APIKey, error) {
	return scanKey(s.pool.QueryRow(ctx, `SELECT `+keyCols+` FROM api_keys k WHERE k.id=$1`, id))
}

// ListKeys returns keys owned by userID, or all keys when userID is "".
func (s *Store) ListKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+keyCols+` FROM api_keys k WHERE $1 = '' OR k.user_id::text = $1 ORDER BY k.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func (s *Store) UpdateKey(ctx context.Context, id string, in KeyInput) (*APIKey, error) {
	return scanKey(s.pool.QueryRow(ctx, `
		UPDATE api_keys k SET alias=$2, group_id=$3, models=$4, budget_nanos=$6, rpm_limit=$7, tpm_limit=$8, expires_at=$9,
			spend_nanos     = CASE WHEN k.budget_type <> $5::budget_type THEN 0 ELSE k.spend_nanos END,
			budget_reset_at = CASE WHEN k.budget_type <> $5::budget_type THEN next_budget_reset($5) ELSE k.budget_reset_at END,
			budget_type=$5
		WHERE k.id=$1 AND k.rotated_at IS NULL AND k.revoked_at IS NULL RETURNING `+keyCols,
		id, in.Alias, in.GroupID, nonNil(in.Models), in.BudgetType, in.BudgetNanos, in.RPMLimit, in.TPMLimit, in.ExpiresAt))
}

func (s *Store) RevokeKey(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AuthByKeyHash is the per-request lookup. One indexed query, no cache, so
// revocations, user disables and model access changes take effect on the next request.
// ponytail: this per-request join is the access-change invalidation; there is no cache to bust.
func (s *Store) AuthByKeyHash(ctx context.Context, hash string) (*AuthInfo, error) {
	var a AuthInfo
	g := &a.Group
	dest := append(keyDest(&a.Key), &a.UserEmail, &a.UserDisabled, &a.GroupModels, &a.ExtraModels,
		&g.ID, &g.Name, &g.BudgetType, &g.BudgetNanos, &g.SpendNanos, &g.BudgetResetAt, &g.RPMLimit, &g.TPMLimit, &g.CreatedAt, &g.UpdatedAt)
	err := s.pool.QueryRow(ctx, `
		SELECT `+keyCols+`, u.email, u.disabled, ug.models, u.extra_models,
			g.id, g.name, g.budget_type, g.budget_nanos, g.spend_nanos, g.budget_reset_at, g.rpm_limit, g.tpm_limit, g.created_at, g.updated_at
		FROM api_keys k JOIN users u ON u.id = k.user_id JOIN groups g ON g.id = k.group_id JOIN groups ug ON ug.id = u.group_id
		WHERE k.key_hash = $1`, hash).Scan(dest...)
	return &a, notFound(err)
}

// CountKeysOutsideAccess counts live keys of enabled owners (in groupID, or userID;
// "" = any) whose explicit models list holds an enabled model the owner can't use.
// Key lists are never rewritten: the gateway refuses those models instead.
func (s *Store) CountKeysOutsideAccess(ctx context.Context, groupID, userID string) (n int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM api_keys k JOIN users u ON u.id = k.user_id JOIN groups g ON g.id = u.group_id
		WHERE ($1 = '' OR u.group_id::text = $1) AND ($2 = '' OR u.id::text = $2)
			AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now()) AND NOT u.disabled
			AND cardinality(g.models) > 0
			AND EXISTS (SELECT 1 FROM unnest(k.models) AS km(name) JOIN models m ON m.name = km.name AND m.enabled
				WHERE km.name <> ALL(g.models) AND km.name <> ALL(u.extra_models))`, groupID, userID).Scan(&n)
	return n, err
}

var ErrKeyNotRotatable = errors.New("key is revoked, expired, or already rotated")

// RotateKey issues a replacement for an active key and retires the old one:
// revoked now when grace is 0, otherwise expiring at min(expires_at, now+grace).
// The new key keeps the old key's owner, limits, spend and expiry. FOR UPDATE
// makes it single-use: a concurrent rotate waits, then sees rotated_at.
func (s *Store) RotateKey(ctx context.Context, id string, grace time.Duration) (string, *APIKey, *APIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	defer tx.Rollback(ctx)

	old, err := scanKey(tx.QueryRow(ctx, `SELECT `+keyCols+` FROM api_keys k WHERE k.id=$1 FOR UPDATE`, id))
	if err != nil {
		return "", nil, nil, err
	}
	if old.RevokedAt != nil || old.RotatedAt != nil || (old.ExpiresAt != nil && !old.ExpiresAt.After(time.Now())) {
		return "", nil, nil, ErrKeyNotRotatable
	}

	raw, hash, prefix := GenerateKey()
	nk, err := scanKey(tx.QueryRow(ctx, `
		INSERT INTO api_keys AS k (key_hash, key_prefix, alias, user_id, group_id, models,
			budget_type, budget_nanos, spend_nanos, budget_reset_at, rpm_limit, tpm_limit, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+keyCols,
		hash, prefix, old.Alias, old.UserID, old.GroupID, nonNil(old.Models),
		old.BudgetType, old.BudgetNanos, old.SpendNanos, old.BudgetResetAt, old.RPMLimit, old.TPMLimit, old.ExpiresAt))
	if err != nil {
		return "", nil, nil, err
	}

	retired, err := scanKey(tx.QueryRow(ctx, `
		UPDATE api_keys k SET rotated_at = now(), alias = k.alias || ' (rotated)',
			revoked_at = CASE WHEN $2::int = 0 THEN now() END,
			expires_at = CASE WHEN $2::int = 0 THEN k.expires_at
				ELSE LEAST(k.expires_at, now() + make_interval(secs => $2::int)) END
		WHERE k.id = $1 RETURNING `+keyCols, id, int32(grace/time.Second)))
	if err != nil {
		return "", nil, nil, err
	}
	return raw, nk, retired, tx.Commit(ctx)
}
