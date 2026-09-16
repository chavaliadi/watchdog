package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence"
	"github.com/chavaliadi/watchdog/internal/state"
)

// Ensure Repository implements persistence.Repository.
var _ persistence.Repository = (*Repository)(nil)

// Repository implements persistence.Repository using standard library database/sql
// against a PostgreSQL database.
type Repository struct {
	db *sql.DB
}

// New constructs a PostgreSQL-backed repository using the provided *sql.DB.
// The caller owns lifecycle management (opening, connection pooling, and closing) of db.
func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

const selectMonitorColumns = `
	id, name, kind, target, method, expected_status_range,
	interval_ms, timeout_ms, enabled, created_at, updated_at
`

type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(s scanner) (monitor.Monitor, error) {
	var (
		mID                 string
		name                string
		kindStr             string
		target              string
		method              sql.NullString
		expectedStatusRange sql.NullString
		intervalMs          int64
		timeoutMs           int64
		enabled             bool
		createdAt           time.Time
		updatedAt           time.Time
	)

	err := s.Scan(
		&mID,
		&name,
		&kindStr,
		&target,
		&method,
		&expectedStatusRange,
		&intervalMs,
		&timeoutMs,
		&enabled,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return monitor.Monitor{}, err
	}

	m := monitor.Monitor{
		ID:        mID,
		Name:      name,
		Kind:      monitor.Kind(kindStr),
		TargetURL: target,
		Interval:  time.Duration(intervalMs) * time.Millisecond,
		Timeout:   time.Duration(timeoutMs) * time.Millisecond,
		Enabled:   enabled,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	if method.Valid {
		m.Method = method.String
	}
	if expectedStatusRange.Valid {
		m.ExpectedStatusRange = expectedStatusRange.String
	}

	return m, nil
}

// GetMonitor retrieves an individual monitor configuration by ID from the monitors table.
// If the monitor row does not exist, sql.ErrNoRows is wrapped and returned.
func (r *Repository) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	query := `
		SELECT ` + selectMonitorColumns + `
		FROM monitors
		WHERE id = $1
	`

	m, err := scanMonitor(r.db.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return monitor.Monitor{}, fmt.Errorf("get monitor %q: %w", id, sql.ErrNoRows)
		}
		return monitor.Monitor{}, fmt.Errorf("get monitor %q: %w", id, err)
	}

	return m, nil
}

// ListMonitors retrieves all monitor configurations ordered deterministically by creation time and ID.
// It returns an empty slice if no monitors exist.
func (r *Repository) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	query := `
		SELECT ` + selectMonitorColumns + `
		FROM monitors
		ORDER BY created_at ASC, id ASC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]monitor.Monitor, 0)
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, fmt.Errorf("scan monitor in list: %w", err)
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list monitors rows iteration: %w", err)
	}

	return monitors, nil
}

// GetStateWithTimestamp retrieves the current health state and last update time of a monitor from monitor_states.
// If no state row exists, sql.ErrNoRows is wrapped and returned.
// If the database contains an invalid/unrecognized state string, an error is returned.
func (r *Repository) GetStateWithTimestamp(ctx context.Context, monitorID string) (state.State, time.Time, error) {
	const query = `
		SELECT state, updated_at
		FROM monitor_states
		WHERE monitor_id = $1
	`

	var (
		rawState  string
		updatedAt time.Time
	)
	err := r.db.QueryRowContext(ctx, query, monitorID).Scan(&rawState, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, fmt.Errorf("get state for monitor %q: %w", monitorID, sql.ErrNoRows)
		}
		return "", time.Time{}, fmt.Errorf("get state for monitor %q: %w", monitorID, err)
	}

	st := state.State(rawState)
	switch st {
	case state.StateUnknown, state.StateHealthy, state.StateUnhealthy:
		return st, updatedAt, nil
	default:
		return "", time.Time{}, fmt.Errorf("invalid persisted state %q for monitor %q", rawState, monitorID)
	}
}

// GetState retrieves the current health state of a monitor from monitor_states.
func (r *Repository) GetState(ctx context.Context, monitorID string) (state.State, error) {
	st, _, err := r.GetStateWithTimestamp(ctx, monitorID)
	return st, err
}

// CreateMonitor creates a new monitor and its initial UNKNOWN health state atomically.
// If either insert fails, the transaction is rolled back so that callers never observe
// an orphaned monitor without its corresponding current state.
func (r *Repository) CreateMonitor(ctx context.Context, m monitor.Monitor) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction for create monitor %q: %w", m.ID, err)
	}
	defer tx.Rollback()

	var method sql.NullString
	if m.Method != "" {
		method = sql.NullString{String: m.Method, Valid: true}
	}

	var expectedStatusRange sql.NullString
	if m.ExpectedStatusRange != "" {
		expectedStatusRange = sql.NullString{String: m.ExpectedStatusRange, Valid: true}
	}

	now := time.Now().UTC()
	createdAt := m.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := now

	intervalMs := m.Interval.Milliseconds()
	if intervalMs <= 0 {
		intervalMs = 60000
	}

	timeoutMs := m.Timeout.Milliseconds()
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	const insertMonitorQuery = `
		INSERT INTO monitors (
			id,
			name,
			kind,
			target,
			method,
			expected_status_range,
			interval_ms,
			timeout_ms,
			enabled,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err = tx.ExecContext(
		ctx,
		insertMonitorQuery,
		m.ID,
		m.Name,
		string(m.Kind),
		m.TargetURL,
		method,
		expectedStatusRange,
		intervalMs,
		timeoutMs,
		m.Enabled,
		createdAt,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert monitor %q: %w", m.ID, err)
	}

	const insertStateQuery = `
		INSERT INTO monitor_states (
			monitor_id,
			state,
			updated_at
		) VALUES ($1, $2, $3)
	`
	_, err = tx.ExecContext(
		ctx,
		insertStateQuery,
		m.ID,
		string(state.StateUnknown),
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert initial state for monitor %q: %w", m.ID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create monitor %q: %w", m.ID, err)
	}

	return nil
}

// SaveCycle persists a completed monitoring check cycle atomically:
// 1. Appends a row to check_results.
// 2. Updates the current state and timestamp in monitor_states.
// If the monitor state does not exist (zero rows updated) or either operation fails,
// the entire transaction rolls back so no orphan check result is persisted.
func (r *Repository) SaveCycle(
	ctx context.Context,
	monitorID string,
	result checker.CheckResult,
	nextState state.State,
) error {
	switch nextState {
	case state.StateUnknown, state.StateHealthy, state.StateUnhealthy:
	default:
		return fmt.Errorf("invalid next state %q for monitor %q", nextState, monitorID)
	}

	checkedAt := result.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	// Status code: CheckResult uses 0 when absent (e.g. TCP or network failures).
	// We map values <= 0 to SQL NULL.
	var statusCode sql.NullInt64
	if result.StatusCode > 0 {
		statusCode = sql.NullInt64{Int64: int64(result.StatusCode), Valid: true}
	}

	var errorClass sql.NullString
	if result.ErrorClass != "" && result.ErrorClass != checker.ErrorClassNone {
		errorClass = sql.NullString{String: string(result.ErrorClass), Valid: true}
	}

	var errorDetail sql.NullString
	if result.ErrorDetail != "" {
		errorDetail = sql.NullString{String: result.ErrorDetail, Valid: true}
	}

	latencyMs := result.Latency.Milliseconds()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction for save cycle on monitor %q: %w", monitorID, err)
	}
	defer tx.Rollback()

	const insertResultQuery = `
		INSERT INTO check_results (
			monitor_id,
			ok,
			status_code,
			latency_ms,
			error_class,
			error_detail,
			attempt_count,
			checked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err = tx.ExecContext(
		ctx,
		insertResultQuery,
		monitorID,
		result.OK,
		statusCode,
		latencyMs,
		errorClass,
		errorDetail,
		result.AttemptCount,
		checkedAt,
	)
	if err != nil {
		return fmt.Errorf("insert check result for monitor %q: %w", monitorID, err)
	}

	const updateStateQuery = `
		UPDATE monitor_states
		SET
			state = $1,
			updated_at = $2
		WHERE monitor_id = $3
	`
	res, err := tx.ExecContext(
		ctx,
		updateStateQuery,
		string(nextState),
		checkedAt,
		monitorID,
	)
	if err != nil {
		return fmt.Errorf("update monitor_states for monitor %q: %w", monitorID, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected for monitor %q: %w", monitorID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("monitor state for monitor %q not found: %w", monitorID, sql.ErrNoRows)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save cycle for monitor %q: %w", monitorID, err)
	}

	return nil
}

// UpdateMonitor updates an existing monitor's configuration.
// If the monitor does not exist, sql.ErrNoRows is wrapped and returned.
func (r *Repository) UpdateMonitor(ctx context.Context, m monitor.Monitor) error {
	var method sql.NullString
	if m.Method != "" {
		method = sql.NullString{String: m.Method, Valid: true}
	}

	var expectedStatusRange sql.NullString
	if m.ExpectedStatusRange != "" {
		expectedStatusRange = sql.NullString{String: m.ExpectedStatusRange, Valid: true}
	}

	intervalMs := m.Interval.Milliseconds()
	if intervalMs <= 0 {
		intervalMs = 60000
	}

	timeoutMs := m.Timeout.Milliseconds()
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}

	now := time.Now().UTC()

	const updateQuery = `
		UPDATE monitors
		SET
			name = $1,
			target = $2,
			method = $3,
			expected_status_range = $4,
			interval_ms = $5,
			timeout_ms = $6,
			enabled = $7,
			updated_at = $8
		WHERE id = $9
	`

	res, err := r.db.ExecContext(
		ctx,
		updateQuery,
		m.Name,
		m.TargetURL,
		method,
		expectedStatusRange,
		intervalMs,
		timeoutMs,
		m.Enabled,
		now,
		m.ID,
	)
	if err != nil {
		return fmt.Errorf("update monitor %q: %w", m.ID, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected for update monitor %q: %w", m.ID, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("update monitor %q: %w", m.ID, sql.ErrNoRows)
	}

	return nil
}

// DeleteMonitor deletes an existing monitor by ID.
// Cascade rules in PostgreSQL automatically clean up associated monitor_states and check_results.
// If the monitor does not exist, sql.ErrNoRows is wrapped and returned.
func (r *Repository) DeleteMonitor(ctx context.Context, id string) error {
	const deleteQuery = `
		DELETE FROM monitors
		WHERE id = $1
	`

	res, err := r.db.ExecContext(ctx, deleteQuery, id)
	if err != nil {
		return fmt.Errorf("delete monitor %q: %w", id, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected for delete monitor %q: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("delete monitor %q: %w", id, sql.ErrNoRows)
	}

	return nil
}

// ListCheckResults returns recent check results for a given monitor ordered by checked_at DESC, id DESC.
// If no check results exist, an empty slice is returned.
func (r *Repository) ListCheckResults(ctx context.Context, monitorID string, limit int) ([]checker.CheckResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	const query = `
		SELECT id, monitor_id, ok, status_code, latency_ms, error_class, error_detail, attempt_count, checked_at
		FROM check_results
		WHERE monitor_id = $1
		ORDER BY checked_at DESC, id DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list check results for monitor %q: %w", monitorID, err)
	}
	defer rows.Close()

	results := make([]checker.CheckResult, 0)
	for rows.Next() {
		var (
			rawID        int64
			mID          string
			ok           bool
			statusCode   sql.NullInt64
			latencyMs    int64
			errorClass   sql.NullString
			errorDetail  sql.NullString
			attemptCount int
			checkedAt    time.Time
		)

		err := rows.Scan(
			&rawID,
			&mID,
			&ok,
			&statusCode,
			&latencyMs,
			&errorClass,
			&errorDetail,
			&attemptCount,
			&checkedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan check result for monitor %q: %w", monitorID, err)
		}

		res := checker.CheckResult{
			ID:           strconv.FormatInt(rawID, 10),
			MonitorID:    mID,
			CheckedAt:    checkedAt,
			OK:           ok,
			Latency:      time.Duration(latencyMs) * time.Millisecond,
			AttemptCount: attemptCount,
		}
		if statusCode.Valid {
			res.StatusCode = int(statusCode.Int64)
		}
		if errorClass.Valid {
			res.ErrorClass = checker.ErrorClass(errorClass.String)
		}
		if errorDetail.Valid {
			res.ErrorDetail = errorDetail.String
		}

		results = append(results, res)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate check results for monitor %q: %w", monitorID, err)
	}

	return results, nil
}

