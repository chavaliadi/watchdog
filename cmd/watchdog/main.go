package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
)

// Config holds runtime configuration for Deployment Watchdog.
type Config struct {
	DatabaseURL string
	MonitorID   string
}

// loadConfig reads required runtime parameters from environment variables.
func loadConfig() (Config, error) {
	dbURL := os.Getenv("WATCHDOG_DATABASE_URL")
	if dbURL == "" {
		return Config{}, errors.New("missing required environment variable WATCHDOG_DATABASE_URL")
	}

	monitorID := os.Getenv("WATCHDOG_MONITOR_ID")
	if monitorID == "" {
		return Config{}, errors.New("missing required environment variable WATCHDOG_MONITOR_ID")
	}

	return Config{
		DatabaseURL: dbURL,
		MonitorID:   monitorID,
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

	return execute(ctx, repo, orch, cfg.MonitorID)
}

func execute(
	ctx context.Context,
	repo persistence.Repository,
	orch *scheduler.Orchestrator,
	monitorID string,
) error {
	m, err := repo.GetMonitor(ctx, monitorID)
	if err != nil {
		return fmt.Errorf("load monitor %q: %w", monitorID, err)
	}
	log.Printf("monitor loaded: id=%s name=%s kind=%s target=%s", m.ID, m.Name, m.Kind, m.TargetURL)

	currentState, err := repo.GetState(ctx, monitorID)
	if err != nil {
		return fmt.Errorf("load state for monitor %q: %w", monitorID, err)
	}
	log.Printf("current state loaded: %s", currentState)

	res, err := orch.RunCycle(ctx, m, currentState)
	if err != nil {
		return fmt.Errorf("run cycle for monitor %q: %w", monitorID, err)
	}

	log.Printf(
		"cycle executed: ok=%v status_code=%d latency=%v attempts=%d current_state=%s next_state=%s transitioned=%v",
		res.CheckResult.OK,
		res.CheckResult.StatusCode,
		res.CheckResult.Latency,
		res.CheckResult.AttemptCount,
		res.TransitionResult.CurrentState,
		res.TransitionResult.NextState,
		res.TransitionResult.Transitioned,
	)

	log.Println("deployment-watchdog completed successfully")
	return nil
}
