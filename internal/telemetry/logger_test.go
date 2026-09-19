package telemetry

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger_DefaultText(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Output: &buf,
	}
	logger := NewLogger(cfg)

	logger.Info("test message", AttrComponent, "test", "key1", "val1")

	output := buf.String()
	if !strings.Contains(output, "level=INFO") {
		t.Errorf("expected level=INFO in text output, got %q", output)
	}
	if !strings.Contains(output, "msg=\"test message\"") && !strings.Contains(output, "msg=test message") {
		t.Errorf("expected test message in text output, got %q", output)
	}
	if !strings.Contains(output, "component=test") {
		t.Errorf("expected component=test in text output, got %q", output)
	}
	if !strings.Contains(output, "key1=val1") {
		t.Errorf("expected key1=val1 in text output, got %q", output)
	}
}

func TestNewLogger_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Level:  "INFO",
		Format: "json",
		Output: &buf,
	}
	logger := NewLogger(cfg)

	logger.Info("json message", AttrComponent, "worker", AttrMonitorID, "mon-123", AttrError, "sample error")

	line := buf.String()
	var fields map[string]any
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		t.Fatalf("failed to parse JSON log output: %v, raw: %q", err, line)
	}

	if fields["level"] != "INFO" {
		t.Errorf("expected level=INFO, got %v", fields["level"])
	}
	if fields["msg"] != "json message" {
		t.Errorf("expected msg='json message', got %v", fields["msg"])
	}
	if fields[AttrComponent] != "worker" {
		t.Errorf("expected component=worker, got %v", fields[AttrComponent])
	}
	if fields[AttrMonitorID] != "mon-123" {
		t.Errorf("expected monitor_id=mon-123, got %v", fields[AttrMonitorID])
	}
	if fields[AttrError] != "sample error" {
		t.Errorf("expected error='sample error', got %v", fields[AttrError])
	}
}

func TestNewLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Level:  "WARN",
		Format: "text",
		Output: &buf,
	}
	logger := NewLogger(cfg)

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()
	if strings.Contains(output, "debug message") {
		t.Errorf("DEBUG message should have been filtered out: %s", output)
	}
	if strings.Contains(output, "info message") {
		t.Errorf("INFO message should have been filtered out: %s", output)
	}
	if !strings.Contains(output, "warn message") {
		t.Errorf("WARN message should be present: %s", output)
	}
	if !strings.Contains(output, "error message") {
		t.Errorf("ERROR message should be present: %s", output)
	}
}

func TestNewLogger_UnsupportedLevelFallback(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Level:  "NONEXISTENT_LEVEL",
		Format: "text",
		Output: &buf,
	}
	logger := NewLogger(cfg)

	output := buf.String()
	if !strings.Contains(output, "unsupported log level") {
		t.Errorf("expected warning about unsupported log level, got %q", output)
	}
	if !strings.Contains(output, "defaulting to INFO") {
		t.Errorf("expected mention of defaulting to INFO, got %q", output)
	}

	// Verify that INFO logs work after fallback
	buf.Reset()
	logger.Info("subsequent info message")
	if !strings.Contains(buf.String(), "subsequent info message") {
		t.Errorf("expected info message to be logged under fallback level INFO")
	}
}

func TestNewLogger_UnsupportedFormatFallback(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Level:  "INFO",
		Format: "UNKNOWN_FORMAT",
		Output: &buf,
	}
	_ = NewLogger(cfg)

	output := buf.String()
	if !strings.Contains(output, "unsupported log format") {
		t.Errorf("expected warning about unsupported log format, got %q", output)
	}
	if !strings.Contains(output, "defaulting to text") {
		t.Errorf("expected mention of defaulting to text, got %q", output)
	}
}

func TestSetup_SetsDefaultLogger(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{
		Level:  "INFO",
		Format: "json",
		Output: &buf,
	}
	_ = Setup(cfg)

	slog.Info("default logger test", AttrComponent, "setup_test")

	line := buf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("expected slog.Default to write JSON to configured output: %v, raw: %q", err, line)
	}
	if parsed["msg"] != "default logger test" {
		t.Errorf("expected msg 'default logger test', got %v", parsed["msg"])
	}
}
