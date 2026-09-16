package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/state"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrInvalidInput = errors.New("invalid argument")
	ErrInvalidID    = errors.New("invalid uuid")
	ErrConflict     = errors.New("resource conflict")
	ErrInternal     = errors.New("internal server error")
)

// SchedulerController abstracts MultiScheduler control operations for the service layer.
type SchedulerController interface {
	StartMonitor(ctx context.Context, m monitor.Monitor) error
	StopMonitor(ctx context.Context, id string) error
	UpdateMonitor(ctx context.Context, m monitor.Monitor) error
}

// MonitorService coordinates monitor management business operations across persistence and scheduler.
type MonitorService struct {
	repo      persistence.Repository
	scheduler SchedulerController
}

// NewMonitorService creates a new MonitorService.
func NewMonitorService(repo persistence.Repository, sched SchedulerController) *MonitorService {
	return &MonitorService{
		repo:      repo,
		scheduler: sched,
	}
}

// CreateMonitorRequest defines parameters for creating a new monitor.
type CreateMonitorRequest struct {
	Name                string
	Kind                monitor.Kind
	Target              string
	Method              string
	ExpectedStatusRange string
	IntervalMs          *int64
	TimeoutMs           *int64
	Enabled             *bool
}

// PatchMonitorRequest defines parameters for partially updating an existing monitor.
type PatchMonitorRequest struct {
	Name                *string
	Kind                *monitor.Kind
	Target              *string
	Method              *string
	ExpectedStatusRange *string
	IntervalMs          *int64
	TimeoutMs           *int64
	Enabled             *bool
}

// MonitorStatus represents the current health state and timestamp for a monitor.
type MonitorStatus struct {
	MonitorID string      `json:"monitor_id"`
	State     state.State `json:"state"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// CreateMonitor validates, persists, and conditionally schedules a new monitor.
func (s *MonitorService) CreateMonitor(ctx context.Context, req CreateMonitorRequest) (monitor.Monitor, error) {
	// Defaults
	intervalMs := int64(60000)
	if req.IntervalMs != nil {
		intervalMs = *req.IntervalMs
	}

	timeoutMs := int64(5000)
	if req.TimeoutMs != nil {
		timeoutMs = *req.TimeoutMs
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	method := req.Method
	if req.Kind == monitor.KindHTTP && strings.TrimSpace(method) == "" {
		method = "GET"
	}

	mID, err := generateUUID()
	if err != nil {
		return monitor.Monitor{}, fmt.Errorf("%w: failed to generate id", ErrInternal)
	}

	now := time.Now().UTC()
	m := monitor.Monitor{
		ID:                  mID,
		Name:                strings.TrimSpace(req.Name),
		Kind:                req.Kind,
		TargetURL:           strings.TrimSpace(req.Target),
		Method:              strings.ToUpper(strings.TrimSpace(method)),
		ExpectedStatusRange: strings.TrimSpace(req.ExpectedStatusRange),
		Interval:            time.Duration(intervalMs) * time.Millisecond,
		Timeout:             time.Duration(timeoutMs) * time.Millisecond,
		Enabled:             enabled,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := validateMonitor(m); err != nil {
		return monitor.Monitor{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// 1. Persist monitor and initial UNKNOWN state atomically
	if err := s.repo.CreateMonitor(ctx, m); err != nil {
		return monitor.Monitor{}, fmt.Errorf("%w: persist monitor: %v", ErrInternal, err)
	}

	// 2. If enabled and scheduler provided, schedule runtime runner
	if m.Enabled && s.scheduler != nil {
		if err := s.scheduler.StartMonitor(ctx, m); err != nil {
			// Compensating rollback: delete newly created monitor row so no orphaned monitor remains
			_ = s.repo.DeleteMonitor(context.Background(), m.ID)
			return monitor.Monitor{}, fmt.Errorf("%w: scheduler start: %v", ErrInternal, err)
		}
	}

	return m, nil
}

// GetMonitor retrieves monitor configuration by ID.
func (s *MonitorService) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	if !isValidUUID(id) {
		return monitor.Monitor{}, ErrInvalidID
	}

	m, err := s.repo.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return monitor.Monitor{}, ErrNotFound
		}
		return monitor.Monitor{}, fmt.Errorf("%w: get monitor: %v", ErrInternal, err)
	}

	return m, nil
}

// ListMonitors retrieves all monitors ordered by created_at ASC, id ASC.
func (s *MonitorService) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	monitors, err := s.repo.ListMonitors(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: list monitors: %v", ErrInternal, err)
	}
	if monitors == nil {
		monitors = make([]monitor.Monitor, 0)
	}
	return monitors, nil
}

// PatchMonitor updates specific fields of an existing monitor and reconciles the scheduler.
func (s *MonitorService) PatchMonitor(ctx context.Context, id string, req PatchMonitorRequest) (monitor.Monitor, error) {
	if !isValidUUID(id) {
		return monitor.Monitor{}, ErrInvalidID
	}

	existing, err := s.repo.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return monitor.Monitor{}, ErrNotFound
		}
		return monitor.Monitor{}, fmt.Errorf("%w: get existing monitor: %v", ErrInternal, err)
	}

	// Immutable field checks: kind cannot be modified
	if req.Kind != nil && *req.Kind != existing.Kind {
		return monitor.Monitor{}, fmt.Errorf("%w: monitor kind is immutable; delete and recreate to change protocol", ErrInvalidInput)
	}

	updated := existing
	if req.Name != nil {
		updated.Name = strings.TrimSpace(*req.Name)
	}
	if req.Target != nil {
		updated.TargetURL = strings.TrimSpace(*req.Target)
	}
	if req.Method != nil {
		updated.Method = strings.ToUpper(strings.TrimSpace(*req.Method))
	}
	if req.ExpectedStatusRange != nil {
		updated.ExpectedStatusRange = strings.TrimSpace(*req.ExpectedStatusRange)
	}
	if req.IntervalMs != nil {
		updated.Interval = time.Duration(*req.IntervalMs) * time.Millisecond
	}
	if req.TimeoutMs != nil {
		updated.Timeout = time.Duration(*req.TimeoutMs) * time.Millisecond
	}
	if req.Enabled != nil {
		updated.Enabled = *req.Enabled
	}
	updated.UpdatedAt = time.Now().UTC()

	// Full domain validation of the resulting configuration
	if err := validateMonitor(updated); err != nil {
		return monitor.Monitor{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	// 1. Update persistence
	if err := s.repo.UpdateMonitor(ctx, updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return monitor.Monitor{}, ErrNotFound
		}
		return monitor.Monitor{}, fmt.Errorf("%w: update monitor in db: %v", ErrInternal, err)
	}

	// 2. Reconcile scheduler
	if s.scheduler != nil {
		if err := s.scheduler.UpdateMonitor(ctx, updated); err != nil {
			// Compensating rollback: restore original DB monitor and scheduler projection
			_ = s.repo.UpdateMonitor(context.Background(), existing)
			_ = s.scheduler.UpdateMonitor(context.Background(), existing)
			return monitor.Monitor{}, fmt.Errorf("%w: scheduler reconcile: %v", ErrInternal, err)
		}
	}

	return updated, nil
}

// DeleteMonitor stops the scheduler runner synchronously and then deletes the monitor from persistence.
func (s *MonitorService) DeleteMonitor(ctx context.Context, id string) error {
	if !isValidUUID(id) {
		return ErrInvalidID
	}

	existing, err := s.repo.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: check monitor existence: %v", ErrInternal, err)
	}

	// 1. Synchronously stop active runner. Guarantees runner is completely terminated before DB deletion.
	if s.scheduler != nil {
		if err := s.scheduler.StopMonitor(ctx, id); err != nil {
			return fmt.Errorf("%w: stop monitor runner: %v", ErrInternal, err)
		}
	}

	// 2. Delete monitor from PostgreSQL (cascades to monitor_states and check_results)
	if err := s.repo.DeleteMonitor(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		// Compensate: if DB deletion failed and monitor was enabled, restart scheduler runner
		if existing.Enabled && s.scheduler != nil {
			_ = s.scheduler.StartMonitor(context.Background(), existing)
		}
		return fmt.Errorf("%w: delete monitor from db: %v", ErrInternal, err)
	}

	return nil
}

// GetStatus retrieves current health state and timestamp for a monitor.
func (s *MonitorService) GetStatus(ctx context.Context, id string) (MonitorStatus, error) {
	if !isValidUUID(id) {
		return MonitorStatus{}, ErrInvalidID
	}

	st, updatedAt, err := s.repo.GetStateWithTimestamp(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MonitorStatus{}, ErrNotFound
		}
		return MonitorStatus{}, fmt.Errorf("%w: get monitor state: %v", ErrInternal, err)
	}

	return MonitorStatus{
		MonitorID: id,
		State:     st,
		UpdatedAt: updatedAt,
	}, nil
}

// ListChecks returns recent check execution history for a monitor ordered by checked_at DESC, id DESC.
func (s *MonitorService) ListChecks(ctx context.Context, id string, limit int) ([]checker.CheckResult, error) {
	if !isValidUUID(id) {
		return nil, ErrInvalidID
	}

	// Verify monitor exists to return 404 for missing monitors
	_, err := s.repo.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: check monitor existence: %v", ErrInternal, err)
	}

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	history, err := s.repo.ListCheckResults(ctx, id, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: list check results: %v", ErrInternal, err)
	}
	if history == nil {
		history = make([]checker.CheckResult, 0)
	}

	return history, nil
}

// validateMonitor performs global and protocol-specific domain validation.
func validateMonitor(m monitor.Monitor) error {
	// Global validation
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("name must not be empty or whitespace-only")
	}
	if len(m.Name) > 255 {
		return errors.New("name must not exceed 255 characters")
	}
	if strings.TrimSpace(m.TargetURL) == "" {
		return errors.New("target is required")
	}
	if m.Interval.Milliseconds() < 1000 {
		return errors.New("interval_ms must be at least 1000ms")
	}
	if m.Timeout.Milliseconds() < 100 {
		return errors.New("timeout_ms must be at least 100ms")
	}

	switch m.Kind {
	case monitor.KindHTTP:
		return validateHTTP(m)
	case monitor.KindTCP:
		return validateTCP(m)
	default:
		return fmt.Errorf("unsupported monitor kind %q, must be 'http' or 'tcp'", m.Kind)
	}
}

func validateHTTP(m monitor.Monitor) error {
	u, err := url.Parse(m.TargetURL)
	if err != nil || u.Host == "" {
		return errors.New("http target must be a valid URL with a host")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("http target scheme must be 'http' or 'https'")
	}

	method := strings.ToUpper(strings.TrimSpace(m.Method))
	switch method {
	case "GET", "POST", "PUT", "HEAD", "DELETE", "PATCH":
		// valid
	default:
		return fmt.Errorf("invalid http method %q, allowed: GET, POST, PUT, HEAD, DELETE, PATCH", m.Method)
	}

	if m.ExpectedStatusRange != "" {
		if err := validateExpectedStatusRange(m.ExpectedStatusRange); err != nil {
			return err
		}
	}

	return nil
}

func validateExpectedStatusRange(r string) error {
	parts := strings.Split(r, "-")
	if len(parts) == 1 {
		code, err := strconv.Atoi(parts[0])
		if err != nil || code < 100 || code > 599 {
			return fmt.Errorf("invalid expected status %q, must be between 100 and 599", parts[0])
		}
		return nil
	}
	if len(parts) == 2 {
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || start < 100 || end > 599 || start > end {
			return fmt.Errorf("invalid expected status range %q, must be A-B with 100 <= A <= B <= 599", r)
		}
		return nil
	}
	return fmt.Errorf("invalid expected status range format %q", r)
}

func validateTCP(m monitor.Monitor) error {
	host, portStr, err := net.SplitHostPort(m.TargetURL)
	if err != nil || host == "" || portStr == "" {
		return errors.New("tcp target must be a valid host:port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("tcp port must be between 1 and 65535")
	}

	if strings.TrimSpace(m.Method) != "" {
		return errors.New("method must be omitted/empty for tcp monitors")
	}
	if strings.TrimSpace(m.ExpectedStatusRange) != "" {
		return errors.New("expected_status_range must be omitted/empty for tcp monitors")
	}

	return nil
}

func isValidUUID(u string) bool {
	if len(u) != 36 {
		return false
	}
	for i, c := range u {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func generateUUID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
