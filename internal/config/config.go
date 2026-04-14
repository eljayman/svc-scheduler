package config

import (
	"github.com/eljayman/mtg-common/config"
)

type Config struct {
	Env             string
	DatabaseURL     string
	HTTPPort        int
	PlannerInterval int // seconds between planner ticks
	WorkerPoolSize  int // number of concurrent run workers
	WorkerInterval  int // seconds between runner polls
}

func Load() Config {
	config.Load(".env")

	cfg := Config{
		Env:             config.String("ENV", "development"),
		DatabaseURL:     config.RequireString("DATABASE_URL"),
		HTTPPort:        config.Int("HTTP_PORT", 8080),
		PlannerInterval: config.Int("PLANNER_INTERVAL_SECONDS", 30),
		WorkerPoolSize:  config.Int("WORKER_POOL_SIZE", 5),
		WorkerInterval:  config.Int("WORKER_INTERVAL_SECONDS", 5),
	}

	config.MustValidate(
		config.NotEmpty("DATABASE_URL", cfg.DatabaseURL),
	)

	return cfg
}
