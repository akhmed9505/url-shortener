package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/akhmed9505/url-shortener/internal/config"
	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/infra/postgres"
	redisinfra "github.com/akhmed9505/url-shortener/internal/infra/redis"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	rediscache "github.com/akhmed9505/url-shortener/internal/repository/redis"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

type App struct {
	log *slog.Logger
	cfg config.Config
}

func New(cfg config.Config, log *slog.Logger) *App {
	return &App{
		log: log,
		cfg: cfg,
	}
}

func (a *App) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pool, err := postgres.NewPool(ctx, a.cfg.Postgres)
	if err != nil {
		return domain.ErrStorageUnavailable
	}
	defer pool.Close()
	a.log.Info("PostgreSQL pool connected")
	client := redisinfra.NewClient(a.cfg.Redis)
	defer client.Close()
	pingCtx, cancel := context.WithTimeout(ctx, config.RedisOperationTimeout)
	err = client.Ping(pingCtx).Err()
	cancel()
	if err != nil {
		a.log.Warn("redis_unavailable")
	}
	cache := rediscache.NewLinkCache(client, a.cfg.Redis.CacheTTL)
	repository := pgrepo.NewLinkRepository(pool)
	visits := pgrepo.NewVisitRepository(pool)
	service := shortener.New(repository, visits, cache, shortener.GenerateCode, a.cfg.AnalyticsWriteTimeout, a.log)
	analyticsService := analytics.New(pgrepo.NewAnalyticsRepository(pool))
	handler := httpdelivery.NewHandler(service, analyticsService, a.cfg.PublicBaseURL, a.log)
	server := httpdelivery.NewServer(a.cfg.HTTP, httpdelivery.NewRouter(handler), a.log)
	serverErrors := make(chan error, 1)
	go func() {
		a.log.Info("starting HTTP server", "addr", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down HTTP server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		closeErr := server.Close()
		return fmt.Errorf("shut down HTTP server: %w", errors.Join(err, closeErr))
	}
	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	a.log.Info("HTTP server stopped")
	return nil
}
