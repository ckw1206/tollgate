package repo

import "context"

// ExecForTest lets tests poke state the API deliberately doesn't expose.
func (s *Store) ExecForTest(ctx context.Context, sql string, args ...any) error {
	_, err := s.pool.Exec(ctx, sql, args...)
	return err
}
