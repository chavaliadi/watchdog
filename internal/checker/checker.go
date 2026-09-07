package checker

import (
	"context"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

// Checker defines the contract for executing a check against a monitored target.
type Checker interface {
	Check(ctx context.Context, m monitor.Monitor) (CheckResult, error)
}
