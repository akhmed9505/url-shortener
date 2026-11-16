package config

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_ADDR", "LOG_LEVEL", "HTTP_READ_HEADER_TIMEOUT",
		"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnvironment(t)
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{HTTP: HTTP{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, LogLevel: slog.LevelInfo}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnvironment(t)
	for key, value := range map[string]string{
		"HTTP_ADDR":                "[::1]:9000",
		"LOG_LEVEL":                "debug",
		"HTTP_READ_HEADER_TIMEOUT": "250ms",
		"HTTP_READ_TIMEOUT":        "2s",
		"HTTP_WRITE_TIMEOUT":       "3s",
		"HTTP_IDLE_TIMEOUT":        "1m30s",
	} {
		t.Setenv(key, value)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{HTTP: HTTP{
		Addr:              "[::1]:9000",
		ReadHeaderTimeout: 250 * time.Millisecond,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       90 * time.Second,
	}, LogLevel: slog.LevelDebug}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidAddress(t *testing.T) {
	for _, address := range []string{"", "8080", ":", ":http", ":0", ":-1", ":65536", "::1:8080"} {
		t.Run(address, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("HTTP_ADDR", address)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
				t.Fatalf("Load() error = %v, want HTTP_ADDR validation error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	for _, level := range []string{"", "verbose"} {
		t.Run(level, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("LOG_LEVEL", level)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
				t.Fatalf("Load() error = %v, want LOG_LEVEL validation error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidTimeouts(t *testing.T) {
	for _, key := range []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT"} {
		for _, value := range []string{"", "0s", "-1s", "10", "invalid", "999999999999999999999h"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				clearEnvironment(t)
				t.Setenv(key, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("Load() error = %v, want %s validation error", err, key)
				}
			})
		}
	}
}

func TestLoadRejectsHeaderTimeoutExceedingReadTimeout(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "11s")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a header timeout exceeding the read timeout")
	}
}
