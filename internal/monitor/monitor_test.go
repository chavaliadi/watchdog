package monitor

import (
	"testing"
)

func TestKindConstants(t *testing.T) {
	tests := []struct {
		kind     Kind
		expected string
	}{
		{KindHTTP, "http"},
		{KindTCP, "tcp"},
		{KindHealth, "health"},
	}

	for _, tt := range tests {
		if string(tt.kind) != tt.expected {
			t.Errorf("expected kind %q, got %q", tt.expected, tt.kind)
		}
	}
}
