package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/worker"
)

// Config holds runtime configuration for Deployment Watchdog.
type Config struct {
	DatabaseURL       string
	WorkerConcurrency int
	HTTPPort          string
}

const (
	defaultWorkerConcurrency = 5
	defaultHTTPPort          = ":8080"
)

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

	httpPort := os.Getenv("WATCHDOG_HTTP_PORT")
	if httpPort == "" {
		httpPort = defaultHTTPPort
	} else if !strings.HasPrefix(httpPort, ":") {
		httpPort = ":" + httpPort
	}

	return Config{
		DatabaseURL:       dbURL,
		WorkerConcurrency: concurrency,
		HTTPPort:          httpPort,
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

	monitorSvc := service.NewMonitorService(repo, multiSched)
	handlers := api.NewHandlers(monitorSvc)
	apiServer := api.NewServer(api.Config{Addr: cfg.HTTPPort}, handlers)

	log.Printf("deployment-watchdog running (concurrency=%d, http_port=%s)...", cfg.WorkerConcurrency, cfg.HTTPPort)
	return execute(ctx, multiSched, apiServer)
}

func execute(ctx context.Context, multiSched *scheduler.MultiScheduler, apiServer *api.Server) error {
	if err := multiSched.Start(ctx); err != nil {
		return fmt.Errorf("multi-scheduler start: %w", err)
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := apiServer.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	var runErr error
	select {
	case <-ctx.Done():
		log.Println("shutdown signal received, initiating graceful shutdown...")
	case err := <-serverErr:
		if err != nil {
			runErr = fmt.Errorf("http server error: %w", err)
			log.Printf("http server error: %v, initiating shutdown...", err)
		}
	}

	// 1. Gracefully shut down HTTP server with bounded timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("error during http server shutdown: %v", err)
	}

	// 2. Stop MultiScheduler runners and wait
	multiSched.Stop()
	multiSched.Wait()

	if runErr != nil {
		return runErr
	}

	log.Println("deployment-watchdog completed successfully")
	return nil
}
