package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/worker"
)

// Config holds runtime configuration for Deployment Watchdog.
type Config struct {
	DatabaseURL       string
	WorkerConcurrency int
}

const defaultWorkerConcurrency = 5

// loadConfig reads required runtime parameters from environment variables.
func loadConfig() (Config, error) {
	dbURL := os.Getenv("WATCHDOG_DATABASE_URL")
	if dbURL == "" {
		return Config{}, errors.New("missing required environment variable WATCHDOG_DATABASE_URL")
	}

	concurrency := defaultWorkerConcurrency
	if val := os.Getenv("WATCHDOG_WORKER_CONCURRENCY"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			concurrency = parsed
		}
	}

	return Config{
		DatabaseURL:       dbURL,
		WorkerConcurrency: concurrency,
	}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Printf("fatal error: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	log.Println("deployment-watchdog starting...")

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("error closing database: %v", closeErr)
		}
	}()

	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}
	log.Println("database connection established")

	var repo persistence.Repository = postgres.New(db)
	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)

	pool, err := worker.NewPool(worker.Config{
		MaxConcurrency: cfg.WorkerConcurrency,
	}, orch)
	if err != nil {
		return fmt.Errorf("create worker pool: %w", err)
	}
	pool.Start(ctx)
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	multiSched, err := scheduler.NewMultiScheduler(repo, pool)
	if err != nil {
		return fmt.Errorf("create multi-scheduler: %w", err)
	}

	log.Printf("deployment-watchdog multi-scheduler running (concurrency=%d)...", cfg.WorkerConcurrency)
	return execute(ctx, multiSched)
}

func execute(ctx context.Context, multiSched *scheduler.MultiScheduler) error {
	if err := multiSched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("multi-scheduler run: %w", err)
	}

	log.Println("deployment-watchdog completed successfully")
	return nil
}
