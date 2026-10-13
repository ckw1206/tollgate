package config

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL        string
	RedisURL           string
	BaseURL            string
	Addr               string
	EncryptionKey      []byte
	TrustedProxies     []netip.Prefix
	CookieInsecure     bool
	SpendRetentionDays int
}

func Load() (*Config, error) {
	c := &Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		BaseURL:     strings.TrimRight(os.Getenv("BASE_URL"), "/"),
		Addr:        ":" + envOr("PORT", "4000"),
	}
	for name, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "BASE_URL": c.BaseURL} {
		if v == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	key, err := hex.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be 64 hex chars (openssl rand -hex 32)")
	}
	c.EncryptionKey = key
	for _, s := range strings.Split(os.Getenv("TRUSTED_PROXIES"), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %w", err)
		}
		c.TrustedProxies = append(c.TrustedProxies, p)
	}
	c.CookieInsecure = os.Getenv("COOKIE_INSECURE") == "true"
	if c.SpendRetentionDays, err = strconv.Atoi(envOr("SPEND_RETENTION_DAYS", "90")); err != nil || c.SpendRetentionDays < 1 {
		return nil, fmt.Errorf("SPEND_RETENTION_DAYS must be a positive integer")
	}
	return c, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
