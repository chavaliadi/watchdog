package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// GetMonitor retrieves an individual monitor configuration by ID from the monitors table.
// If the monitor row does not exist, sql.ErrNoRows is wrapped and returned.
func (r *Repository) GetMonitor(ctx context.Context, id string) (monitor.Monitor, error) {
	const query = `
		SELECT id, name, kind, target, method, expected_status_range, created_at, updated_at
		FROM monitors
		WHERE id = $1
	`

	var (
		mID                 string
		name                string
		kindStr             string
		target              string
		method              sql.NullString
		expectedStatusRange sql.NullString
		createdAt           time.Time
		updatedAt           time.Time
	)

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&mID,
		&name,
		&kindStr,
		&target,
		&method,
		&expectedStatusRange,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return monitor.Monitor{}, fmt.Errorf("get monitor %q: %w", id, sql.ErrNoRows)
		}
		return monitor.Monitor{}, fmt.Errorf("get monitor %q: %w", id, err)
	}

	m := monitor.Monitor{
		ID:        mID,
		Name:      name,
		Kind:      monitor.Kind(kindStr),
		TargetURL: target,
		CreatedAt: createdAt,
	}

	if method.Valid {
		m.Method = method.String
	}
	if expectedStatusRange.Valid {
		m.ExpectedStatusRange = expectedStatusRange.String
	}

	return m, nil
}

// GetState retrieves the current health state of a monitor from monitor_states.
// If no state row exists, sql.ErrNoRows is wrapped and returned.
// If the database contains an invalid/unrecognized state string, an error is returned.
func (r *Repository) GetState(ctx context.Context, monitorID string) (state.State, error) {
	const query = `
		SELECT state
		FROM monitor_states
		WHERE monitor_id = $1
	`

	var rawState string
	err := r.db.QueryRowContext(ctx, query, monitorID).Scan(&rawState)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("get state for monitor %q: %w", monitorID, sql.ErrNoRows)
		}
		return "", fmt.Errorf("get state for monitor %q: %w", monitorID, err)
	}

	st := state.State(rawState)
	switch st {
	case state.StateUnknown, state.StateHealthy, state.StateUnhealthy:
		return st, nil
	default:
		return "", fmt.Errorf("invalid persisted state %q for monitor %q", rawState, monitorID)
	}
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

	const insertMonitorQuery = `
		INSERT INTO monitors (
			id,
			name,
			kind,
			target,
			method,
			expected_status_range,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
