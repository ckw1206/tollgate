package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type SpendRecord struct {
	// KeyID empty (key-less, e.g. playground) is stored as NULL and charges
	// the group only; Source marks the origin ("api" default, "playground").
	KeyID            string    `json:"key_id"`
	Source           string    `json:"source"`
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
	KeyAlias   string `json:"key_alias"`
	KeyPrefix  string `json:"key_prefix"`
	OwnerEmail string `json:"owner_email"`
}

type SpendFilter struct {
	UserID, GroupID, KeyID string
	From, To               time.Time
	GroupBy                string // hour | day | week | month | year | model | key | user | group | model,<granularity>
}

type SpendRow struct {
	Bucket       string `json:"bucket"`
	Day          string `json:"day,omitempty"` // group_by=model,day only; Bucket is then the model
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostNanos    int64  `json:"cost_nanos"`
}

// serverTimezone loads the server's timezone from settings, falling back to
// UTC for unknown/invalid names (UpdateSettings validates, but the column can
// hold a name whose zone file is missing on this host — e.g. in a
// distroless image without tzdata — and an arithmetic panic in the spend
// path is not acceptable).
func serverTimezone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil || name == "" {
		return time.UTC
	}
	return loc
}

// RecordSpend writes a batch of spend logs and bumps the key/group spend
// counters in one transaction, so logs and budgets never disagree. Every
// record is logged, but a key/group's spend_nanos only accrues the cost of the
// records that fall inside its current budget window (total/unlimited use the
// fixed epoch window, so every record counts).
//
// The counter UPDATE rolls the window inline when it is already due (the reset
// worker may not have run yet): the old counter is discarded, the new cost
// starts the fresh window, and budget_reset_at advances — spend in the gap
// between the window ending and the reset running is neither lost nor piled
// onto a stale counter (A3). Crossings are detected with effOld = 0 for a
// rolled window, so a first write into a fresh window still fires 80%/100% in
// the gap. ResetBudgets remains defense-in-depth and is a no-op after an
// inline roll.
func (s *Store) RecordSpend(ctx context.Context, recs []SpendRecord) ([]BudgetCrossing, error) {
	if len(recs) == 0 {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows := make([][]any, len(recs))
	keySet, groupSet := map[string]bool{}, map[string]bool{}
	for i, r := range recs {
		var keyID, errMsg any
		if r.KeyID != "" {
			keyID = r.KeyID
			keySet[r.KeyID] = true
		}
		if r.ErrorMessage != "" {
			errMsg = r.ErrorMessage
		}
		source := r.Source
		if source == "" {
			source = "api"
		}
		rows[i] = []any{keyID, r.UserID, r.GroupID, r.ModelID, r.ModelName, r.Provider,
			r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.CostNanos,
			r.LatencyMS, r.Status, r.HTTPStatus, errMsg, r.CreatedAt, source}
		groupSet[r.GroupID] = true
	}

	keyLims, keyLabels, err := budgetWindows(ctx, tx, "api_keys", idsFromSet(keySet))
	if err != nil {
		return nil, err
	}
	groupLims, groupLabels, err := budgetWindows(ctx, tx, "groups", idsFromSet(groupSet))
	if err != nil {
		return nil, err
	}

	st, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	loc := serverTimezone(st.Timezone)

	now := time.Now()
	keyCost := inWindowCosts(recs, keyLims, func(r SpendRecord) string { return r.KeyID }, now, loc)
	groupCost := inWindowCosts(recs, groupLims, func(r SpendRecord) string { return r.GroupID }, now, loc)

	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"spend_logs"}, []string{
		"key_id", "user_id", "group_id", "model_id", "model_name", "provider",
		"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cost_nanos",
		"latency_ms", "status", "http_status", "error_message", "created_at", "source"}, pgx.CopyFromRows(rows)); err != nil {
		return nil, fmt.Errorf("copy spend_logs: %w", err)
	}
	keyRolled, err := rollSpend(ctx, tx, "api_keys", keyLims, keyCost, now)
	if err != nil {
		return nil, err
	}
	groupRolled, err := rollSpend(ctx, tx, "groups", groupLims, groupCost, now)
	if err != nil {
		return nil, err
	}

	// Crossing detection runs after the counter UPDATEs, before commit, using
	// the in-window costs fetched above. For a window this batch just rolled
	// inline, effOld is 0 (reset_at in the past) and the batch cost IS the
	// fresh window's spend, so effNew is the post-roll counter value.
	crossings, err := s.detectCrossings(ctx, tx, "api_keys", "key", keyLims, keyLabels, keyCost, now, loc, keyRolled)
	if err != nil {
		return nil, err
	}
	gx, err := s.detectCrossings(ctx, tx, "groups", "group", groupLims, groupLabels, groupCost, now, loc, groupRolled)
	if err != nil {
		return nil, err
	}
	crossings = append(crossings, gx...)

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return crossings, nil
}

// rollSpend adds each scope's in-window cost to its spend counter, rolling
// the window inline when it is due (A3): a windowed budget past its reset
// instant starts a fresh window at zero and reschedules budget_reset_at, so
// the gap before the reset worker runs neither erases this spend (the stale
// counter is discarded) nor lets it pile onto the old window's counter.
// Non-windowed budgets and budgets that are not yet due just accumulate.
// Runs in the caller's transaction, so the spend_logs insert and the counter
// update commit or roll back together. Returns, for every scope that was due
// before this write, the fresh window's budget_reset_at (the window identity
// crossing detection must use).
func rollSpend(ctx context.Context, tx pgx.Tx, table string, lims map[string]Limits, costs map[string]int64, now time.Time) (map[string]time.Time, error) {
	rolled := map[string]time.Time{}
	lastUsed := ""
	if table == "api_keys" {
		lastUsed = "last_used_at = now(),"
	}
	for id, l := range lims {
		var newReset *time.Time
		err := tx.QueryRow(ctx,
			`UPDATE `+table+` SET `+lastUsed+`
			 spend_nanos = CASE WHEN budget_type IN ('daily','monthly') AND budget_reset_at <= now()
				 THEN $2 ELSE spend_nanos + $2 END,
			 budget_reset_at = CASE WHEN budget_type IN ('daily','monthly') AND budget_reset_at <= now()
				 THEN next_budget_reset(budget_type) ELSE budget_reset_at END
			 WHERE id = $1
			 RETURNING budget_reset_at`, id, costs[id]).Scan(&newReset)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the row vanished between budgetWindows and here; nothing to charge
			}
			return nil, err
		}
		// Rolled iff the row was due before this write — the same condition
		// the SQL CASE applies — and the RETURNING value is then the fresh
		// window's reset instant. (RETURNING reflects post-update values, so
		// the due-ness must be judged against the pre-batch limits.)
		if l.isWindowed() && l.BudgetResetAt != nil && !now.Before(*l.BudgetResetAt) && newReset != nil {
			rolled[id] = *newReset
		}
	}
	return rolled, nil
}

// detectCrossings fires a one-shot BudgetCrossing for each scope whose
// effective spend crosses the 80% or 100% threshold in its current window.
// effOld is the pre-batch effective spend (0 for an expired-but-not-yet-reset
// window — the inline roll discards the stale counter in this same
// transaction, so the fresh window's spend starts at exactly this batch's
// cost); effNew is effOld plus this batch's in-window cost. Scopes with no cap
// and unlimited budgets are skipped; total budgets use the fixed epoch window
// identity so their one-shot marker works (A4). The marker UPDATE is atomic:
// a threshold fires only if the stored notify window differs from this
// window's start OR the stored pct is below the threshold, so each threshold
// fires once per window.
//
// The marker's window identity is the window the scope is IN right now: the
// expired-window start (the fresh window began at the reset that just passed)
// for a write that rolled the window inline, the active window's start
// otherwise. This equals what a later write in the fresh window derives once
// the stored counter has rolled, for the standard short gap the reset worker
// is expected to cover.
func (s *Store) detectCrossings(ctx context.Context, tx pgx.Tx, table, scope string,
	lims map[string]Limits, labels map[string]string, costs map[string]int64, now time.Time, loc *time.Location, rolled map[string]time.Time) ([]BudgetCrossing, error) {
	var out []BudgetCrossing
	for id, lim := range lims {
		ws := lim.WindowStart(now, loc)
		if ws == nil || lim.BudgetNanos <= 0 || lim.BudgetType == "unlimited" {
			continue // no window identity, no cap, or no threshold: never crosses
		}
		effOld := lim.EffectiveSpend(now)
		effNew := effOld + costs[id]
		resetAt := lim.BudgetResetAt
		if r, ok := rolled[id]; ok {
			resetAt = &r // report the fresh window's reset instant, not the one that just passed
		}
		for _, t := range []int64{80, 100} {
			thr := t * lim.BudgetNanos / 100
			if thr <= 0 || effOld >= thr || effNew < thr {
				continue
			}
			// The WHERE guard below stays as-is. pct is stored per-window: when
			// this write re-arms a brand-new window (the stored window start
			// differs from $2), reset pct to the current threshold $t so it
			// starts fresh; within the same window keep the monotonic GREATEST
			// so 80 then 100 still both fire and never regress. Without this,
			// a chronically-maxed key stuck at pct 100 in window A would re-arm
			// at 80% in window B but leave pct at 100, making the 100% guard
			// (window IS DISTINCT FROM ws OR pct < 100) false — dropping the
			// next window's 100% (#24).
			tag, err := tx.Exec(ctx,
				`UPDATE `+table+` SET budget_notified_window = $2, budget_notified_pct =
					CASE WHEN budget_notified_window IS DISTINCT FROM $2 THEN $3
						ELSE GREATEST(budget_notified_pct, $3) END
				 WHERE id = $1 AND (budget_notified_window IS DISTINCT FROM $2 OR budget_notified_pct < $3)`,
				id, ws, int16(t))
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 1 {
				out = append(out, BudgetCrossing{
					Scope:         scope,
					ID:            id,
					Label:         labels[id],
					Threshold:     int(t),
					SpendNanos:    effNew,
					BudgetNanos:   lim.BudgetNanos,
					BudgetType:    lim.BudgetType,
					BudgetResetAt: resetAt,
				})
			}
		}
	}
	return out, nil
}

// budgetWindows fetches the window fields and human label for the given ids
// from table (api_keys or groups). One query per scope type; these tables are
// small. The label (alias for keys, name for groups) is returned alongside so
// crossing reports can name the scope without exposing a raw key/hash.
func budgetWindows(ctx context.Context, q pgx.Tx, table string, ids []string) (map[string]Limits, map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	labelCol := "name"
	if table == "api_keys" {
		labelCol = "alias"
	}
	rows, err := q.Query(ctx,
		`SELECT id, `+labelCol+`, budget_type, budget_nanos, spend_nanos, budget_reset_at FROM `+table+
			` WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := make(map[string]Limits, len(ids))
	labels := make(map[string]string, len(ids))
	for rows.Next() {
		var id, label string
		var l Limits
		if err := rows.Scan(&id, &label, &l.BudgetType, &l.BudgetNanos, &l.SpendNanos, &l.BudgetResetAt); err != nil {
			return nil, nil, err
		}
		out[id] = l
		labels[id] = label
	}
	return out, labels, rows.Err()
}

// inWindowCosts returns each scope id's in-window cost. Every distinct id in
// recs is present (value 0 if all its records are out of window), so callers
// still touch last_used_at for a key/group that saw only stale records. A
// record counts when its scope has no window (nil BudgetResetAt on a windowed
// type) or its CreatedAt is at/after the window start — the epoch sentinel
// for total/unlimited admits every record.
func inWindowCosts(recs []SpendRecord, lims map[string]Limits, scope func(SpendRecord) string, now time.Time, loc *time.Location) map[string]int64 {
	out := map[string]int64{}
	for _, r := range recs {
		id := scope(r)
		if _, ok := out[id]; !ok {
			out[id] = 0
		}
		ws := lims[id].WindowStart(now, loc)
		if ws == nil || !r.CreatedAt.Before(*ws) {
			out[id] += r.CostNanos
		}
	}
	return out
}

// idsFromSet converts an id set to a slice for a query argument.
func idsFromSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}

// ResetBudgets zeroes every daily/monthly window that's due and schedules the next one.
// It does NOT touch the one-shot notify marker: the marker keys on the window start
// (see detectCrossings), which advances by one period when the reset runs, so its
// IS DISTINCT FROM guard re-arms naturally for the new window. Clearing the marker
// here would open a double-notify: a late reset that zeroes spend and clears the
// marker would let the same window's threshold fire a second time (#24).
//
// After RecordSpend's inline roll (A3) the window is already advanced and its
// counter fresh, so this is a no-op for those rows — defense-in-depth for
// scopes that saw no writes since their window expired.
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

// spendDay buckets by calendar day in the server timezone.
const spendDay = `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'YYYY-MM-DD')`

// spendHour buckets by hour in the server timezone (label: YYYY-MM-DDTHH, 24h).
// HH24, not HH — Postgres 'HH' is a 12-hour clock and would wrap 17:05 to 05,
// breaking both the C1 24-hour label and lexicographic sort order.
const spendHour = `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'YYYY-MM-DD"T"HH24')`

// spendWeek buckets by ISO week in the server timezone (label: IYYY-Www, e.g. 2026-W42).
const spendWeek = `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'IYYY-"W"IW')`

// spendMonth buckets by month in the server timezone (label: YYYY-MM).
const spendMonth = `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'YYYY-MM')`

// spendYear buckets by year in the server timezone (label: YYYY).
const spendYear = `to_char(created_at AT TIME ZONE (SELECT timezone FROM server_settings WHERE id=1), 'YYYY')`

var ErrGroupBy = errors.New("group_by must be hour, day, week, month, year, model, key, user, group, or model,<hour|day|week|month|year>")

func (s *Store) QuerySpend(ctx context.Context, f SpendFilter) ([]SpendRow, error) {
	bucket := map[string]string{
		"hour":        spendHour,
		"day":         spendDay,
		"week":        spendWeek,
		"month":       spendMonth,
		"year":        spendYear,
		"model":       `model_name`,
		"model,day":   `model_name`,
		"model,hour":  `model_name`,
		"model,week":  `model_name`,
		"model,month": `model_name`,
		"model,year":  `model_name`,
		"key":         `COALESCE(key_id::text,'playground')`,
		"user":        `user_id::text`,
		"group":       `group_id::text`,
	}[f.GroupBy]
	if bucket == "" {
		return nil, ErrGroupBy
	}
	// model,<granularity> pairs the model (Bucket) with a time sub-bucket (Day);
	// the label format follows the granularity — the same mechanism as model,day.
	day := `''`
	switch f.GroupBy {
	case "model,day":
		day = spendDay
	case "model,hour":
		day = spendHour
	case "model,week":
		day = spendWeek
	case "model,month":
		day = spendMonth
	case "model,year":
		day = spendYear
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+bucket+` AS b, `+day+` AS d, count(*), sum(input_tokens + cache_read_tokens + cache_write_tokens), sum(output_tokens), sum(cost_nanos)
		FROM spend_logs `+spendWhere+` GROUP BY b, d ORDER BY d, b`,
		f.From, f.To, f.UserID, f.GroupID, f.KeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpendRow
	for rows.Next() {
		var r SpendRow
		if err := rows.Scan(&r.Bucket, &r.Day, &r.Requests, &r.InputTokens, &r.OutputTokens, &r.CostNanos); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentSpend returns the newest logs in the filter, with the key's alias and prefix and the
// owner's email. Key-less (playground) rows show up as the "Playground" key with a "session"
// prefix. spend_logs is limited in a subquery first, so spendWhere's columns stay unambiguous.
func (s *Store) RecentSpend(ctx context.Context, f SpendFilter, limit int) ([]SpendLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.key_id, l.user_id, l.group_id, l.model_id, l.model_name, l.provider, l.input_tokens, l.output_tokens,
			l.cache_read_tokens, l.cache_write_tokens, l.cost_nanos, l.latency_ms, l.status, l.http_status, COALESCE(l.error_message, ''), l.created_at, l.source,
			COALESCE(k.alias, 'Playground'), COALESCE(k.key_prefix, 'session'), u.email
		FROM (SELECT * FROM spend_logs `+spendWhere+` ORDER BY created_at DESC, id DESC LIMIT $6) l
		LEFT JOIN api_keys k ON k.id = l.key_id JOIN users u ON u.id = l.user_id
		ORDER BY l.created_at DESC, l.id DESC`,
		f.From, f.To, f.UserID, f.GroupID, f.KeyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpendLog
	for rows.Next() {
		var l SpendLog
		var keyID *string
		if err := rows.Scan(&l.ID, &keyID, &l.UserID, &l.GroupID, &l.ModelID, &l.ModelName, &l.Provider,
			&l.InputTokens, &l.OutputTokens, &l.CacheReadTokens, &l.CacheWriteTokens, &l.CostNanos,
			&l.LatencyMS, &l.Status, &l.HTTPStatus, &l.ErrorMessage, &l.CreatedAt, &l.Source, &l.KeyAlias, &l.KeyPrefix, &l.OwnerEmail); err != nil {
			return nil, err
		}
		if keyID != nil {
			l.KeyID = *keyID
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
