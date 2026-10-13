package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// The minute is part of the key name, so a window can never outlive its minute
// even though EXPIRE is refreshed on every hit.
func rpmKey(scope, id string, now time.Time) string {
	return fmt.Sprintf("rl:rpm:%s:%s:%d", scope, id, now.Unix()/60)
}

func tpmKey(scope, id string, now time.Time) string {
	return fmt.Sprintf("rl:tpm:%s:%s:%d", scope, id, now.Unix()/60)
}

const windowTTL = 120 * time.Second

func (c *Cache) HitRPM(ctx context.Context, scope, id string, now time.Time) (int64, error) {
	k := rpmKey(scope, id, now)
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, windowTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

func (c *Cache) TPM(ctx context.Context, scope, id string, now time.Time) (int64, error) {
	n, err := c.rdb.Get(ctx, tpmKey(scope, id, now)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

func (c *Cache) AddTPM(ctx context.Context, scope, id string, tokens int64, now time.Time) error {
	k := tpmKey(scope, id, now)
	pipe := c.rdb.TxPipeline()
	pipe.IncrBy(ctx, k, tokens)
	pipe.Expire(ctx, k, windowTTL)
	_, err := pipe.Exec(ctx)
	return err
}
