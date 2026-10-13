package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
}

func New(url string) (*Cache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return FromClient(redis.NewClient(opts)), nil
}

func FromClient(c *redis.Client) *Cache { return &Cache{rdb: c} }

func (c *Cache) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

func (c *Cache) Close() error { return c.rdb.Close() }
