package redis

import (
	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/redis/go-redis/v9"
)

func NewClient(cfg config.Redis) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:       cfg.Addr,
		Username:   cfg.Username,
		Password:   cfg.Password,
		DB:         cfg.DB,
		Protocol:   2,
		MaxRetries: -1,
		// DialerRetries counts attempts, so one means no retry.
		DialerRetries:         1,
		DialTimeout:           config.RedisOperationTimeout,
		ReadTimeout:           config.RedisOperationTimeout,
		WriteTimeout:          config.RedisOperationTimeout,
		PoolTimeout:           config.RedisOperationTimeout,
		ContextTimeoutEnabled: true,
		PoolSize:              10,
		MaxActiveConns:        10,
		PipelinePoolSize:      -1,
		DisableIdentity:       true,
	})
}
