// Сервер «Покер на костях»: REST API поверх SQL-функций PostgreSQL.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/seventwogth/databases-itmo/db"
	"github.com/seventwogth/databases-itmo/internal/auth"
	"github.com/seventwogth/databases-itmo/internal/httpapi"
	"github.com/seventwogth/databases-itmo/internal/store"
)

const devSecret = "dev-secret-change-me"

type config struct {
	addr        string
	databaseURL string
	jwtSecret   string
	tokenTTL    time.Duration
	migrate     bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func loadConfig() (config, error) {
	ttl, err := time.ParseDuration(env("TOKEN_TTL", "24h"))
	if err != nil {
		return config{}, errors.New("TOKEN_TTL: " + err.Error())
	}
	migrate, err := strconv.ParseBool(env("MIGRATE", "true"))
	if err != nil {
		return config{}, errors.New("MIGRATE: " + err.Error())
	}
	return config{
		addr:        env("HTTP_ADDR", ":8080"),
		databaseURL: env("DATABASE_URL", "postgres://poker:poker@localhost:5432/poker?sslmode=disable"),
		jwtSecret:   env("JWT_SECRET", devSecret),
		tokenTTL:    ttl,
		migrate:     migrate,
	}, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if cfg.jwtSecret == devSecret {
		slog.Warn("JWT_SECRET не задан — используется небезопасный dev-секрет")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connect(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.migrate {
		applied, err := store.Migrate(ctx, pool, db.Migrations, "migrations")
		if err != nil {
			return err
		}
		slog.Info("migrations", "applied", applied)
	}

	api := httpapi.New(store.New(pool), auth.NewTokens(cfg.jwtSecret, cfg.tokenTTL))
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.addr, "swagger", "http://localhost"+portOf(cfg.addr)+"/swagger/index.html")
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// connect ждёт готовности БД (актуально при старте рядом с контейнером Postgres).
func connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return pool, nil
		}
		if attempt == 15 {
			pool.Close()
			return nil, err
		}
		slog.Warn("database not ready, retrying", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return ""
}
