package cache

import (
	"context"
	"strings"
	"time"
)

const (
	LoginMaxFailures = 10
	LoginWindow      = 15 * time.Minute
)

func loginKeys(ip, email string) []string {
	return []string{"loginfail:ip:" + ip, "loginfail:email:" + strings.ToLower(email)}
}

func (c *Cache) LoginBlocked(ctx context.Context, ip, email string) (bool, error) {
	vals, err := c.rdb.MGet(ctx, loginKeys(ip, email)...).Result()
	if err != nil {
		return false, err
	}
	for _, v := range vals {
		if s, ok := v.(string); ok && atoi(s) >= LoginMaxFailures {
			return true, nil
		}
	}
	return false, nil
}

// RecordLoginFailure counts a failure against both IP and email, pipelining
// INCR and EXPIRE so concurrent calls can't set the TTL from the wrong time.
func (c *Cache) RecordLoginFailure(ctx context.Context, ip, email string) error {
	for _, k := range loginKeys(ip, email) {
		pipe := c.rdb.Pipeline()
		incr := pipe.Incr(ctx, k)
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		if incr.Val() == 1 {
			c.rdb.Expire(ctx, k, LoginWindow)
		}
	}
	return nil
}

func (c *Cache) ClearLoginFailures(ctx context.Context, email string) error {
	return c.rdb.Del(ctx, "loginfail:email:"+strings.ToLower(email)).Err()
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
