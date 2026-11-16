package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type Config struct {
	HTTP     HTTP
	LogLevel slog.Level
	Postgres Postgres
}

type Postgres struct {
	DSN            string
	MaxConns       int32
	MinConns       int32
	ConnectTimeout time.Duration
}

type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

func Load() (Config, error) {
	cfg := Config{HTTP: HTTP{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, LogLevel: slog.LevelInfo, Postgres: Postgres{
		DSN:            "postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable",
		MaxConns:       10,
		MinConns:       0,
		ConnectTimeout: 5 * time.Second,
	}}

	if dsn, ok := os.LookupEnv("POSTGRES_DSN"); ok {
		cfg.Postgres.DSN = dsn
	}
	if cfg.Postgres.DSN == "" {
		return Config{}, fmt.Errorf("POSTGRES_DSN must not be empty")
	}
	if _, err := pgx.ParseConfig(cfg.Postgres.DSN); err != nil {
		return Config{}, fmt.Errorf("POSTGRES_DSN must be a valid PostgreSQL connection string")
	}
	for _, setting := range []struct {
		name  string
		value *int32
	}{
		{"POSTGRES_MAX_CONNS", &cfg.Postgres.MaxConns},
		{"POSTGRES_MIN_CONNS", &cfg.Postgres.MinConns},
	} {
		if raw, ok := os.LookupEnv(setting.name); ok {
			value, err := strconv.ParseInt(raw, 10, 32)
			if err != nil || value < 0 {
				return Config{}, fmt.Errorf("%s must be a non-negative int32", setting.name)
			}
			*setting.value = int32(value)
		}
	}
	if cfg.Postgres.MaxConns == 0 || cfg.Postgres.MinConns > cfg.Postgres.MaxConns {
		return Config{}, fmt.Errorf("PostgreSQL pool requires 0 <= POSTGRES_MIN_CONNS <= POSTGRES_MAX_CONNS and POSTGRES_MAX_CONNS > 0")
	}

	if addr, ok := os.LookupEnv("HTTP_ADDR"); ok {
		cfg.HTTP.Addr = addr
	}
	_, port, err := net.SplitHostPort(cfg.HTTP.Addr)
	if err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR must be a host:port address: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("HTTP_ADDR port must be between 1 and 65535")
	}

	if level, ok := os.LookupEnv("LOG_LEVEL"); ok {
		if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
			return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
		}
	}

	for _, setting := range []struct {
		name  string
		value *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", &cfg.HTTP.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", &cfg.HTTP.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", &cfg.HTTP.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", &cfg.HTTP.IdleTimeout},
		{"POSTGRES_CONNECT_TIMEOUT", &cfg.Postgres.ConnectTimeout},
	} {
		if raw, ok := os.LookupEnv(setting.name); ok {
			duration, err := time.ParseDuration(raw)
			if err != nil || duration <= 0 {
				return Config{}, fmt.Errorf("%s must be a positive duration", setting.name)
			}
			*setting.value = duration
		}
	}
	if cfg.HTTP.ReadHeaderTimeout > cfg.HTTP.ReadTimeout {
		return Config{}, fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must not exceed HTTP_READ_TIMEOUT")
	}
	return cfg, nil
}
