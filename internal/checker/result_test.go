package checker

import (
	"testing"
)

func TestErrorClassConstants(t *testing.T) {
	tests := []struct {
		errClass ErrorClass
		expected string
	}{
		{ErrorClassNone, ""},
		{ErrorClassDNS, "dns"},
		{ErrorClassConnRefused, "conn_refused"},
		{ErrorClassTLS, "tls"},
		{ErrorClassTimeout, "timeout"},
		{ErrorClassStatus, "status"},
		{ErrorClassAssertion, "assertion"},
	}

	for _, tt := range tests {
		if string(tt.errClass) != tt.expected {
			t.Errorf("expected ErrorClass %q, got %q", tt.expected, tt.errClass)
		}
	}
}
