package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/eljayman/mtg-common/database"
	"github.com/eljayman/mtg-common/logging"
	"github.com/eljayman/mtg-common/server"
	"github.com/eljayman/svc-scheduler/internal/api"
	"github.com/eljayman/svc-scheduler/internal/config"
	"github.com/eljayman/svc-scheduler/internal/planner"
	"github.com/eljayman/svc-scheduler/internal/runner"
)

func main() {
	cfg := config.Load()
	logger := logging.New(cfg.Env, "svc-scheduler")

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Database
	pool, err := database.Connect(ctx, database.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		logger.Error("failed to connect to database", slog.Any("error", err))
		os.Exit(1)
	}
	defer pool.Close()

	if err := database.RunMigrations(cfg.DatabaseURL, "file://migrations"); err != nil {
		logger.Error("failed to run migrations", slog.Any("error", err))
		os.Exit(1)
	}

	// Start planner
	p := planner.New(pool, cfg.PlannerInterval, logger)
	go p.Run(ctx)

	// Start runner
	r := runner.New(pool, cfg.WorkerPoolSize, cfg.WorkerInterval, logger)
	go r.Run(ctx)

	// Admin API
	adminToken := os.Getenv("ADMIN_TOKEN")
	router := server.NewRouter(logger, "", 100, 20)
	router.Mount("/admin", api.Router(pool, adminToken, logger))

	httpCfg := server.DefaultHTTPConfig(cfg.HTTPPort)
	if err := server.RunHTTP(ctx, httpCfg, router, logger); err != nil {
		logger.Error("http server error", slog.Any("error", err))
		os.Exit(1)
	}
}
