package persistence

import (
	"context"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/state"
)

// Repository defines the contract for persisting and retrieving monitors,
// monitor health states, and cycle check results.
type Repository interface {
	GetMonitor(ctx context.Context, id string) (monitor.Monitor, error)
	GetState(ctx context.Context, monitorID string) (state.State, error)
	CreateMonitor(ctx context.Context, m monitor.Monitor) error
	SaveCycle(
		ctx context.Context,
		monitorID string,
		result checker.CheckResult,
		nextState state.State,
	) error
}
