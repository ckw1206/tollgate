package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sessions are keyed by the hash of the cookie token, so a Redis dump doesn't hand out live sessions.
func (c *Cache) SetSession(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	return c.rdb.Set(ctx, "sess:"+tokenHash, userID, ttl).Err()
}

func (c *Cache) GetSession(ctx context.Context, tokenHash string) (string, error) {
	id, err := c.rdb.Get(ctx, "sess:"+tokenHash).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return id, err
}

func (c *Cache) DelSession(ctx context.Context, tokenHash string) error {
	return c.rdb.Del(ctx, "sess:"+tokenHash).Err()
}
