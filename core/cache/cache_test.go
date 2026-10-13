package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTest(t *testing.T) (*Cache, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)
	return FromClient(redis.NewClient(&redis.Options{Addr: mr.Addr()})), mr
}

func TestHitRPM_CountsPerMinuteWindow(t *testing.T) {
	c, mr := newTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_800_000_000, 0) // minute boundary irrelevant; same minute below
	for i := int64(1); i <= 3; i++ {
		n, err := c.HitRPM(ctx, "key", "k1", t0)
		if err != nil || n != i {
			t.Fatalf("hit %d: n=%d err=%v", i, n, err)
		}
	}
	if n, _ := c.HitRPM(ctx, "key", "k1", t0.Add(time.Minute)); n != 1 {
		t.Fatalf("next minute should start at 1, got %d", n)
	}
	if n, _ := c.HitRPM(ctx, "group", "k1", t0); n != 1 {
		t.Fatalf("scopes must not share counters, got %d", n)
	}
	key := rpmKey("key", "k1", t0)
	if ttl := mr.TTL(key); ttl <= 0 || ttl > 120*time.Second {
		t.Fatalf("ttl=%v", ttl)
	}
}

func TestTPM_AddAndRead(t *testing.T) {
	c, _ := newTest(t)
	ctx := context.Background()
	now := time.Now()
	if n, _ := c.TPM(ctx, "key", "k1", now); n != 0 {
		t.Fatalf("empty TPM = %d", n)
	}
	c.AddTPM(ctx, "key", "k1", 700, now)
	c.AddTPM(ctx, "key", "k1", 300, now)
	if n, _ := c.TPM(ctx, "key", "k1", now); n != 1000 {
		t.Fatalf("TPM = %d", n)
	}
}

func TestSessions(t *testing.T) {
	c, mr := newTest(t)
	ctx := context.Background()
	c.SetSession(ctx, "h1", "user-1", time.Hour)
	if id, _ := c.GetSession(ctx, "h1"); id != "user-1" {
		t.Fatalf("got %q", id)
	}
	mr.FastForward(2 * time.Hour)
	if id, _ := c.GetSession(ctx, "h1"); id != "" {
		t.Fatal("session should expire")
	}
	c.SetSession(ctx, "h2", "user-2", time.Hour)
	c.DelSession(ctx, "h2")
	if id, _ := c.GetSession(ctx, "h2"); id != "" {
		t.Fatal("session should be deleted")
	}
}

func TestLoginThrottle_ByIPAndByEmail(t *testing.T) {
	c, mr := newTest(t)
	ctx := context.Background()
	for i := 0; i < LoginMaxFailures; i++ {
		c.RecordLoginFailure(ctx, "1.2.3.4", "victim@x.io")
	}
	if b, _ := c.LoginBlocked(ctx, "9.9.9.9", "victim@x.io"); !b {
		t.Fatal("email lock must apply from any IP")
	}
	if b, _ := c.LoginBlocked(ctx, "1.2.3.4", "other@x.io"); !b {
		t.Fatal("IP lock must apply to any email")
	}
	if b, _ := c.LoginBlocked(ctx, "9.9.9.9", "other@x.io"); b {
		t.Fatal("unrelated ip+email blocked")
	}
	mr.FastForward(LoginWindow + time.Second)
	if b, _ := c.LoginBlocked(ctx, "1.2.3.4", "victim@x.io"); b {
		t.Fatal("lock should expire")
	}
}
