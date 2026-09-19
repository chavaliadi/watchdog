package telemetry

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Standard structured log attribute keys to ensure consistency across the application.
const (
	AttrComponent = "component"
	AttrMonitorID = "monitor_id"
	AttrCycleID   = "cycle_id"
	AttrRequestID = "req_id"
	AttrKind      = "kind"
	AttrError     = "error"
)

// Config defines the configuration for the application structured logger.
type Config struct {
	Level  string
	Format string
	Output io.Writer
}

// ConfigFromEnv reads logging configuration from environment variables:
// - WATCHDOG_LOG_LEVEL (default: INFO)
// - WATCHDOG_LOG_FORMAT (default: text)
func ConfigFromEnv() Config {
	return Config{
		Level:  os.Getenv("WATCHDOG_LOG_LEVEL"),
		Format: os.Getenv("WATCHDOG_LOG_FORMAT"),
		Output: os.Stderr,
	}
}

// parseLevel parses a level string into a slog.Level.
// If the level is unknown or empty, it returns slog.LevelInfo and a warning flag.
func parseLevel(raw string) (slog.Level, string) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	switch trimmed {
	case "":
		return slog.LevelInfo, ""
	case "DEBUG":
		return slog.LevelDebug, ""
	case "INFO":
		return slog.LevelInfo, ""
	case "WARN", "WARNING":
		return slog.LevelWarn, ""
	case "ERROR":
		return slog.LevelError, ""
	default:
		return slog.LevelInfo, "unsupported log level: " + raw + ", defaulting to INFO"
	}
}

// parseFormat parses a format string into "text" or "json".
// If the format is unknown or empty, it returns "text" and a warning flag.
func parseFormat(raw string) (string, string) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "":
		return "text", ""
	case "text":
		return "text", ""
	case "json":
		return "json", ""
	default:
		return "text", "unsupported log format: " + raw + ", defaulting to text"
	}
}

// NewLogger constructs a new *slog.Logger based on the provided configuration.
// If warnings occur during level or format parsing, they are emitted to the configured logger.
func NewLogger(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stderr
	}

	level, levelWarn := parseLevel(cfg.Level)
	format, formatWarn := parseFormat(cfg.Format)

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}

	logger := slog.New(handler)

	// Emit configuration warnings if invalid values were supplied
	if levelWarn != "" {
		logger.Warn(levelWarn, AttrComponent, "telemetry")
	}
	if formatWarn != "" {
		logger.Warn(formatWarn, AttrComponent, "telemetry")
	}

	return logger
}

// Setup initializes the application logger and sets it as the default slog logger.
// It returns the initialized logger for direct usage.
func Setup(cfg Config) *slog.Logger {
	logger := NewLogger(cfg)
	slog.SetDefault(logger)
	return logger
}
