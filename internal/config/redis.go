package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

const RedisOperationTimeout = 50 * time.Millisecond

type Redis struct {
	Addr     string
	Username string
	Password string
	DB       int
	CacheTTL time.Duration
}

func loadRedis() (Redis, error) {
	cfg := Redis{Addr: "localhost:6379", CacheTTL: time.Hour}
	if addr, ok := os.LookupEnv("REDIS_ADDR"); ok {
		cfg.Addr = addr
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return Redis{}, fmt.Errorf("REDIS_ADDR must be a host:port address")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return Redis{}, fmt.Errorf("REDIS_ADDR port must be between 1 and 65535")
	}
	cfg.Username = os.Getenv("REDIS_USERNAME")
	cfg.Password = os.Getenv("REDIS_PASSWORD")
	if raw, ok := os.LookupEnv("REDIS_DB"); ok {
		db, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || db < 0 {
			return Redis{}, fmt.Errorf("REDIS_DB must be a non-negative int32")
		}
		cfg.DB = int(db)
	}
	if raw, ok := os.LookupEnv("REDIS_CACHE_TTL"); ok {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl < time.Millisecond {
			return Redis{}, fmt.Errorf("REDIS_CACHE_TTL must be a duration of at least 1ms")
		}
		cfg.CacheTTL = ttl
	}
	return cfg, nil
}
