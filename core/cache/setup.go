package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// First-run setup has its own throttle, keyed by IP only. It never touches the
// sign-in counters (loginfail:*), so nobody can lock an email out of signing in
// through the setup form.
const (
	SetupMaxFailures = 5
	SetupWindow      = 15 * time.Minute
)

func setupKey(ip string) string { return "setupfail:ip:" + ip }

func (c *Cache) SetupBlocked(ctx context.Context, ip string) (bool, error) {
	v, err := c.rdb.Get(ctx, setupKey(ip)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return atoi(v) >= SetupMaxFailures, nil
}

// RecordSetupFailure counts a wrong setup token against the IP; the window starts
// at the first failure.
func (c *Cache) RecordSetupFailure(ctx context.Context, ip string) error {
	n, err := c.rdb.Incr(ctx, setupKey(ip)).Result()
	if err != nil {
		return err
	}
	if n == 1 {
		return c.rdb.Expire(ctx, setupKey(ip), SetupWindow).Err()
	}
	return nil
}
