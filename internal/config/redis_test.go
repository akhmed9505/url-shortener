package config

import (
	"strings"
	"testing"
	"time"
)

func TestRedisOverrides(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("REDIS_ADDR", "[::1]:6380")
	t.Setenv("REDIS_USERNAME", "cache")
	t.Setenv("REDIS_PASSWORD", "private-secret")
	t.Setenv("REDIS_DB", "2")
	t.Setenv("REDIS_CACHE_TTL", "30m")
	cfg, err := Load()
	want := Redis{Addr: "[::1]:6380", Username: "cache", Password: "private-secret", DB: 2, CacheTTL: 30 * time.Minute}
	if err != nil || cfg.Redis != want {
		t.Fatal("Redis overrides were not loaded")
	}
}

func TestRejectsInvalidRedisConfig(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"REDIS_ADDR", ""}, {"REDIS_ADDR", "localhost"}, {"REDIS_ADDR", "localhost:0"}, {"REDIS_ADDR", "localhost:65536"}, {"REDIS_ADDR", "redis://private-secret@localhost:6379"},
		{"REDIS_DB", ""}, {"REDIS_DB", "-1"}, {"REDIS_DB", "2147483648"}, {"REDIS_DB", "invalid"},
		{"REDIS_CACHE_TTL", ""}, {"REDIS_CACHE_TTL", "0s"}, {"REDIS_CACHE_TTL", "-1s"}, {"REDIS_CACHE_TTL", "1ns"}, {"REDIS_CACHE_TTL", "invalid"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "private-secret") {
				t.Fatalf("unexpected validation error %v", err)
			}
		})
	}
}
