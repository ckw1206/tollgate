package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SpendRecord struct {
	KeyID            string    `json:"key_id"`
	UserID           string    `json:"user_id"`
	GroupID          string    `json:"group_id"`
	ModelID          string    `json:"model_id"`
	ModelName        string    `json:"model_name"`
	Provider         string    `json:"provider"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"cache_read_tokens"`
	CacheWriteTokens int64     `json:"cache_write_tokens"`
	CostNanos        int64     `json:"cost_nanos"`
	LatencyMS        int64     `json:"latency_ms"`
	Status           string    `json:"status"`
	HTTPStatus       int       `json:"http_status"`
	ErrorMessage     string    `json:"error_message"`
	CreatedAt        time.Time `json:"created_at"`
}

type SpendLog struct {
	ID int64 `json:"id"`
	SpendRecord
}

type SpendFilter struct {
	UserID, GroupID, KeyID string
	From, To               time.Time
	GroupBy                string // day | model | key | user
}

type SpendRow struct {
	Bucket       string `json:"bucket"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostNanos    int64  `json:"cost_nanos"`
}

// RecordSpend writes a batch of spend logs and bumps the key/group spend
// counters in one transaction, so logs and budgets never disagree.
func (s *Store) RecordSpend(ctx context.Context, recs []SpendRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows := make([][]any, len(recs))
	keyCost, groupCost := map[string]int64{}, map[string]int64{}
	for i, r := range recs {
		var errMsg any
		if r.ErrorMessage != "" {
			errMsg = r.ErrorMessage
		}
		rows[i] = []any{r.KeyID, r.UserID, r.GroupID, r.ModelID, r.ModelName, r.Provider,
			r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.CostNanos,
			r.LatencyMS, r.Status, r.HTTPStatus, errMsg, r.CreatedAt}
		keyCost[r.KeyID] += r.CostNanos
		groupCost[r.GroupID] += r.CostNanos
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"spend_logs"}, []string{
		"key_id", "user_id", "group_id", "model_id", "model_name", "provider",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cost_nanos",
		"latency_ms", "status", "http_status", "error_message", "created_at"}, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy spend_logs: %w", err)
	}
	for id, c := range keyCost {
		if _, err := tx.Exec(ctx, `UPDATE api_keys SET spend_nanos = spend_nanos + $2, last_used_at = now() WHERE id=$1`, id, c); err != nil {
			return err
		}
	}
	for id, c := range groupCost {
		if _, err := tx.Exec(ctx, `UPDATE groups SET spend_nanos = spend_nanos + $2 WHERE id=$1`, id, c); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ResetBudgets zeroes every daily/monthly window that's due and schedules the next one.
func (s *Store) ResetBudgets(ctx context.Context) (int64, error) {
	var total int64
	for _, table := range []string{"api_keys", "groups"} {
		tag, err := s.pool.Exec(ctx, `UPDATE `+table+` SET spend_nanos = 0, budget_reset_at = next_budget_reset(budget_type)
			WHERE budget_type IN ('daily','monthly') AND budget_reset_at <= now()`)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

func (s *Store) DeleteSpendBefore(ctx context.Context, t time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM spend_logs WHERE created_at < $1`, t)
	return tag.RowsAffected(), err
}

const spendWhere = `WHERE created_at >= $1 AND created_at < $2
	AND ($3 = '' OR user_id::text = $3) AND ($4 = '' OR group_id::text = $4) AND ($5 = '' OR key_id::text = $5)`

func (s *Store) QuerySpend(ctx context.Context, f SpendFilter) ([]SpendRow, error) {
	bucket := map[string]string{
		"day":   `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'YYYY-MM-DD')`,
		"model": `model_name`,
		"key":   `key_id::text`,
		"user":  `user_id::text`,
	}[f.GroupBy]
	if bucket == "" {
		return nil, fmt.Errorf("group_by must be day, model, key, or user")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+bucket+` AS b, count(*), sum(input_tokens + cache_read_tokens + cache_write_tokens), sum(output_tokens), sum(cost_nanos)
		FROM spend_logs `+spendWhere+` GROUP BY b ORDER BY b`,
		f.From, f.To, f.UserID, f.GroupID, f.KeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpendRow
	for rows.Next() {
		var r SpendRow
		if err := rows.Scan(&r.Bucket, &r.Requests, &r.InputTokens, &r.OutputTokens, &r.CostNanos); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RecentSpend(ctx context.Context, f SpendFilter, limit int) ([]SpendLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, key_id, user_id, group_id, model_id, model_name, provider, input_tokens, output_tokens,
			cache_read_tokens, cache_write_tokens, cost_nanos, latency_ms, status, http_status, COALESCE(error_message, ''), created_at
		FROM spend_logs `+spendWhere+` ORDER BY id DESC LIMIT $6`,
		f.From, f.To, f.UserID, f.GroupID, f.KeyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpendLog
	for rows.Next() {
		var l SpendLog
		if err := rows.Scan(&l.ID, &l.KeyID, &l.UserID, &l.GroupID, &l.ModelID, &l.ModelName, &l.Provider,
			&l.InputTokens, &l.OutputTokens, &l.CacheReadTokens, &l.CacheWriteTokens, &l.CostNanos,
			&l.LatencyMS, &l.Status, &l.HTTPStatus, &l.ErrorMessage, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
