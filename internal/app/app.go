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
)

type App struct {
	server *http.Server
	log    *slog.Logger
}

func New(cfg config.Config, log *slog.Logger) *App {
	return &App{
		server: httpdelivery.NewServer(cfg.HTTP, httpdelivery.NewRouter(), log),
		log:    log,
	}
}

func (a *App) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	serverErrors := make(chan error, 1)
	go func() {
		a.log.Info("starting HTTP server", "addr", a.server.Addr)
		serverErrors <- a.server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down HTTP server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		closeErr := a.server.Close()
		return fmt.Errorf("shut down HTTP server: %w", errors.Join(err, closeErr))
	}
	if err := <-serverErrors; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	a.log.Info("HTTP server stopped")
	return nil
}
