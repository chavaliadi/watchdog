package checker

import (
	"context"
	"testing"

	"github.com/chavaliadi/watchdog/internal/monitor"
)

type testChecker struct{}

func (testChecker) Check(ctx context.Context, m monitor.Monitor) (CheckResult, error) {
	return CheckResult{}, nil
}

// Compile-time conformance check.
var _ Checker = testChecker{}

func TestCheckerInterfaceConformance(t *testing.T) {
	var c Checker = testChecker{}
	if c == nil {
		t.Fatal("expected non-nil checker")
	}
}
