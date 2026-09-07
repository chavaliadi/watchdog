package state

import (
	"testing"
)

func TestStateConstants(t *testing.T) {
	tests := []struct {
		state    State
		expected string
	}{
		{StateUnknown, "UNKNOWN"},
		{StateUp, "UP"},
		{StateDown, "DOWN"},
		{StateDegraded, "DEGRADED"},
		{StateFlapping, "FLAPPING"},
	}

	for _, tt := range tests {
		if string(tt.state) != tt.expected {
			t.Errorf("expected State %q, got %q", tt.expected, tt.state)
		}
	}
}
