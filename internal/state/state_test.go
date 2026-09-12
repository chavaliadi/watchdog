package state

import (
	"testing"

	"github.com/chavaliadi/watchdog/internal/checker"
)

func TestStateConstants(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateUnknown, "UNKNOWN"},
		{StateHealthy, "HEALTHY"},
		{StateUnhealthy, "UNHEALTHY"},
	}

	for _, tt := range tests {
		if string(tt.state) != tt.expected {
			t.Errorf("expected State %q, got %q", tt.expected, tt.state)
		}
	}
}

func TestTransition_AllPaths(t *testing.T) {
	tests := []struct {
		name                 string
		current              State
		ok                   bool
		expectedNext         State
		expectedTransitioned bool
	}{
		{
			name:                 "UNKNOWN + success -> HEALTHY",
			current:              StateUnknown,
			ok:                   true,
			expectedNext:         StateHealthy,
			expectedTransitioned: true,
		},
		{
			name:                 "UNKNOWN + failure -> UNHEALTHY",
			current:              StateUnknown,
			ok:                   false,
			expectedNext:         StateUnhealthy,
			expectedTransitioned: true,
		},
		{
			name:                 "HEALTHY + success -> HEALTHY",
			current:              StateHealthy,
			ok:                   true,
			expectedNext:         StateHealthy,
			expectedTransitioned: false,
		},
		{
			name:                 "HEALTHY + failure -> UNHEALTHY",
			current:              StateHealthy,
			ok:                   false,
			expectedNext:         StateUnhealthy,
			expectedTransitioned: true,
		},
		{
			name:                 "UNHEALTHY + success -> HEALTHY",
			current:              StateUnhealthy,
			ok:                   true,
			expectedNext:         StateHealthy,
			expectedTransitioned: true,
		},
		{
			name:                 "UNHEALTHY + failure -> UNHEALTHY",
			current:              StateUnhealthy,
			ok:                   false,
			expectedNext:         StateUnhealthy,
			expectedTransitioned: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Transition(tt.current, tt.ok)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if res.CurrentState != tt.current {
				t.Errorf("expected CurrentState %q, got %q", tt.current, res.CurrentState)
			}
			if res.NextState != tt.expectedNext {
				t.Errorf("expected NextState %q, got %q", tt.expectedNext, res.NextState)
			}
			if res.Transitioned != tt.expectedTransitioned {
				t.Errorf("expected Transitioned %v, got %v", tt.expectedTransitioned, res.Transitioned)
			}
		})
	}
}

func TestTransition_ZeroValueTreatedAsUnknown(t *testing.T) {
	t.Run("empty string + success -> HEALTHY", func(t *testing.T) {
		res, err := Transition("", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.NextState != StateHealthy {
			t.Errorf("expected NextState %q, got %q", StateHealthy, res.NextState)
		}
		if !res.Transitioned {
			t.Errorf("expected transition from zero-value (UNKNOWN) to HEALTHY")
		}
	})

	t.Run("empty string + failure -> UNHEALTHY", func(t *testing.T) {
		res, err := Transition("", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.NextState != StateUnhealthy {
			t.Errorf("expected NextState %q, got %q", StateUnhealthy, res.NextState)
		}
		if !res.Transitioned {
			t.Errorf("expected transition from zero-value (UNKNOWN) to UNHEALTHY")
		}
	})
}

func TestTransition_InvalidState(t *testing.T) {
	invalidStates := []State{
		"DEGRADED",
		"MAINTENANCE",
		"PAUSED",
		"UP",
		"DOWN",
		"FLAPPING",
		"UNKNOWN_STATE",
	}

	for _, inv := range invalidStates {
		t.Run(string(inv), func(t *testing.T) {
			_, err := Transition(inv, true)
			if err == nil {
				t.Errorf("expected error for invalid state %q, got nil", inv)
			}
			_, err = Transition(inv, false)
			if err == nil {
				t.Errorf("expected error for invalid state %q, got nil", inv)
			}
		})
	}
}

func TestTransitionFromCheckResult(t *testing.T) {
	t.Run("delegates CheckResult.OK true", func(t *testing.T) {
		checkRes := checker.CheckResult{
			OK:         true,
			StatusCode: 200,
		}
		res, err := TransitionFromCheckResult(StateHealthy, checkRes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.NextState != StateHealthy {
			t.Errorf("expected NextState %q, got %q", StateHealthy, res.NextState)
		}
		if res.Transitioned {
			t.Errorf("expected Transitioned to be false")
		}
	})

	t.Run("delegates CheckResult.OK false", func(t *testing.T) {
		checkRes := checker.CheckResult{
			OK:         false,
			ErrorClass: checker.ErrorClassTimeout,
		}
		res, err := TransitionFromCheckResult(StateHealthy, checkRes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.NextState != StateUnhealthy {
			t.Errorf("expected NextState %q, got %q", StateUnhealthy, res.NextState)
		}
		if !res.Transitioned {
			t.Errorf("expected Transitioned to be true")
		}
	})
}
