package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTP     HTTP
	LogLevel slog.Level
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
	}, LogLevel: slog.LevelInfo}

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
