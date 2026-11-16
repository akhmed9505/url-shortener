package redis

import (
	"testing"

	"github.com/akhmed9505/url-shortener/internal/config"
)

func TestClientBoundsTimeoutsAndDisablesRetries(t *testing.T) {
	client := NewClient(config.Redis{Addr: "localhost:6379", Username: "user", Password: "password", DB: 2})
	defer client.Close()
	options := client.Options()
	if options.MaxRetries != 0 || options.DialerRetries != 1 || !options.ContextTimeoutEnabled {
		t.Fatal("client retries or deadline propagation misconfigured")
	}
	if options.DialTimeout != config.RedisOperationTimeout || options.ReadTimeout != config.RedisOperationTimeout || options.WriteTimeout != config.RedisOperationTimeout || options.PoolTimeout != config.RedisOperationTimeout {
		t.Fatal("Redis waits must be bounded to 50ms")
	}
	if options.Addr != "localhost:6379" || options.Username != "user" || options.Password != "password" || options.DB != 2 {
		t.Fatal("Redis configuration not applied")
	}
}
