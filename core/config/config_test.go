package config

import "testing"

func setBase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("REDIS_URL", "redis://x")
	t.Setenv("ENCRYPTION_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("BASE_URL", "https://gw.example.com/")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":4000" || c.SpendRetentionDays != 90 || c.CookieInsecure {
		t.Fatalf("bad defaults: %+v", c)
	}
	if c.BaseURL != "https://gw.example.com" {
		t.Fatalf("trailing slash not trimmed: %q", c.BaseURL)
	}
	if len(c.EncryptionKey) != 32 || c.EncryptionKey[31] != 0x1f {
		t.Fatalf("key not decoded")
	}
}

func TestLoad_RejectsBadKey(t *testing.T) {
	setBase(t)
	t.Setenv("ENCRYPTION_KEY", "abcd")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	setBase(t)
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoad_TrustedProxies(t *testing.T) {
	setBase(t)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 172.16.0.0/12")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].String() != "172.16.0.0/12" {
		t.Fatalf("got %v", c.TrustedProxies)
	}
}
