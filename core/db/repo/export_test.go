package repo

import "context"

// ExecForTest lets tests poke state the API deliberately doesn't expose.
func (s *Store) ExecForTest(ctx context.Context, sql string, args ...any) error {
	_, err := s.pool.Exec(ctx, sql, args...)
	return err
}

// QueryForTest reads a single column of a single row the same way.
func (s *Store) QueryForTest(ctx context.Context, sql string, args ...any) (any, error) {
	var out any
	return out, s.pool.QueryRow(ctx, sql, args...).Scan(&out)
}
