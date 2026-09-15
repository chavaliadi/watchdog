package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/state"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	var cleanup func()

	if dbURL == "" {
		var err error
		dbURL, cleanup, err = startEphemeralPostgres()
		if err != nil {
			log.Fatalf("failed to start ephemeral postgres for testing: %v", err)
		}
	}

	var err error
	testDB, err = sql.Open("pgx", dbURL)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		log.Fatalf("failed to open test database: %v", err)
	}

	if err := testDB.Ping(); err != nil {
		if cleanup != nil {
			cleanup()
		}
		log.Fatalf("failed to ping test database: %v", err)
	}

	// Apply schema migration
	schemaPath := filepath.Join("..", "..", "..", "migrations", "001_initial_schema.up.sql")
	schemaSQL, err := os.ReadFile(schemaPath)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		log.Fatalf("failed to read schema file %q: %v", schemaPath, err)
	}

	if _, err := testDB.Exec(string(schemaSQL)); err != nil {
		if cleanup != nil {
			cleanup()
		}
		log.Fatalf("failed to execute initial schema: %v", err)
	}

	code := m.Run()

	_ = testDB.Close()
	if cleanup != nil {
		cleanup()
	}

	os.Exit(code)
}

func startEphemeralPostgres() (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "watchdog_pg_test_*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	dataDir := filepath.Join(tempDir, "data")
	port, err := getFreePort()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("find free port: %w", err)
	}

	initCmd := exec.Command("initdb", "-D", dataDir, "-U", "postgres", "--auth=trust", "-A", "trust")
	if out, err := initCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("initdb failed: %s: %w", string(out), err)
	}

	logFile := filepath.Join(tempDir, "postgres.log")
	pgOptions := fmt.Sprintf("-k '' -h 127.0.0.1 -p %d", port)
	startCmd := exec.Command("pg_ctl", "-D", dataDir, "-o", pgOptions, "-l", logFile, "start")
	if out, err := startCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("pg_ctl start failed: %s: %w", string(out), err)
	}

	createDbCmd := exec.Command("createdb", "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", "postgres", "watchdog_test")
	if out, err := createDbCmd.CombinedOutput(); err != nil {
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("createdb failed: %s: %w", string(out), err)
	}

	cleanup := func() {
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
	}

	connStr := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/watchdog_test?sslmode=disable", port)
	return connStr, cleanup, nil
}

func getFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func cleanTables(t *testing.T) {
	t.Helper()
	_, err := testDB.Exec("TRUNCATE TABLE monitors CASCADE;")
	if err != nil {
		t.Fatalf("truncate tables failed: %v", err)
	}
}

func TestGetMonitor(t *testing.T) {
	repo := postgres.New(testDB)
	ctx := context.Background()

	t.Run("Existing HTTP monitor loads correctly", func(t *testing.T) {
		cleanTables(t)

		id := "11111111-1111-1111-1111-111111111111"
		m := monitor.Monitor{
			ID:                  id,
			Name:                "API Health",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://example.com/health",
			Method:              "GET",
			ExpectedStatusRange: "200-299",
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		got, err := repo.GetMonitor(ctx, id)
		if err != nil {
			t.Fatalf("GetMonitor failed: %v", err)
		}

		if got.ID != m.ID {
			t.Errorf("ID mismatch: got %q, want %q", got.ID, m.ID)
		}
		if got.Name != m.Name {
			t.Errorf("Name mismatch: got %q, want %q", got.Name, m.Name)
		}
		if got.Kind != m.Kind {
			t.Errorf("Kind mismatch: got %q, want %q", got.Kind, m.Kind)
		}
		if got.TargetURL != m.TargetURL {
			t.Errorf("TargetURL mismatch: got %q, want %q", got.TargetURL, m.TargetURL)
		}
		if got.Method != m.Method {
			t.Errorf("Method mismatch: got %q, want %q", got.Method, m.Method)
		}
		if got.ExpectedStatusRange != m.ExpectedStatusRange {
			t.Errorf("ExpectedStatusRange mismatch: got %q, want %q", got.ExpectedStatusRange, m.ExpectedStatusRange)
		}
		if got.CreatedAt.IsZero() {
			t.Error("CreatedAt should not be zero")
		}
	})

	t.Run("Missing monitor returns sql.ErrNoRows", func(t *testing.T) {
		cleanTables(t)

		_, err := repo.GetMonitor(ctx, "00000000-0000-0000-0000-000000000000")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected error wrapping sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("NULL HTTP/TCP optional fields are handled correctly", func(t *testing.T) {
		cleanTables(t)

		id := "22222222-2222-2222-2222-222222222222"
		m := monitor.Monitor{
			ID:        id,
			Name:      "Redis TCP",
			Kind:      monitor.KindTCP,
			TargetURL: "127.0.0.1:6379",
			// Method and ExpectedStatusRange left empty -> mapped to SQL NULL
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		got, err := repo.GetMonitor(ctx, id)
		if err != nil {
			t.Fatalf("GetMonitor failed: %v", err)
		}

		if got.Method != "" {
			t.Errorf("expected empty Method for TCP monitor, got %q", got.Method)
		}
		if got.ExpectedStatusRange != "" {
			t.Errorf("expected empty ExpectedStatusRange for TCP monitor, got %q", got.ExpectedStatusRange)
		}
	})
}

func TestGetState(t *testing.T) {
	repo := postgres.New(testDB)
	ctx := context.Background()

	t.Run("UNKNOWN loads correctly", func(t *testing.T) {
		cleanTables(t)
		id := "33333333-3333-3333-3333-333333333333"
		m := monitor.Monitor{ID: id, Name: "M3", Kind: monitor.KindHTTP, TargetURL: "https://ex.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateUnknown {
			t.Errorf("expected UNKNOWN, got %q", st)
		}
	})

	t.Run("HEALTHY loads correctly", func(t *testing.T) {
		cleanTables(t)
		id := "44444444-4444-4444-4444-444444444444"
		m := monitor.Monitor{ID: id, Name: "M4", Kind: monitor.KindHTTP, TargetURL: "https://ex.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		if err := repo.SaveCycle(ctx, id, checker.CheckResult{
			MonitorID:    id,
			OK:           true,
			StatusCode:   200,
			Latency:      50 * time.Millisecond,
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		}, state.StateHealthy); err != nil {
			t.Fatalf("SaveCycle failed: %v", err)
		}

		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateHealthy {
			t.Errorf("expected HEALTHY, got %q", st)
		}
	})

	t.Run("UNHEALTHY loads correctly", func(t *testing.T) {
		cleanTables(t)
		id := "55555555-5555-5555-5555-555555555555"
		m := monitor.Monitor{ID: id, Name: "M5", Kind: monitor.KindHTTP, TargetURL: "https://ex.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		if err := repo.SaveCycle(ctx, id, checker.CheckResult{
			MonitorID:    id,
			OK:           false,
			StatusCode:   500,
			Latency:      120 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "500 Internal Server Error",
			AttemptCount: 3,
			CheckedAt:    time.Now().UTC(),
		}, state.StateUnhealthy); err != nil {
			t.Fatalf("SaveCycle failed: %v", err)
		}

		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateUnhealthy {
			t.Errorf("expected UNHEALTHY, got %q", st)
		}
	})

	t.Run("Missing state returns sql.ErrNoRows", func(t *testing.T) {
		cleanTables(t)

		_, err := repo.GetState(ctx, "00000000-0000-0000-0000-000000000000")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("expected error wrapping sql.ErrNoRows, got %v", err)
		}
	})

	t.Run("Invalid persisted state returns an error rather than silently becoming UNKNOWN", func(t *testing.T) {
		cleanTables(t)
		id := "66666666-6666-6666-6666-666666666666"
		m := monitor.Monitor{ID: id, Name: "M6", Kind: monitor.KindHTTP, TargetURL: "https://ex.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		// Corrupt the state row in DB to an invalid state string
		_, err := testDB.Exec("UPDATE monitor_states SET state = 'DEGRADED' WHERE monitor_id = $1", id)
		if err != nil {
			t.Fatalf("corrupt state failed: %v", err)
		}

		st, err := repo.GetState(ctx, id)
		if err == nil {
			t.Fatalf("expected error for invalid state, got nil (returned state %q)", st)
		}
		if st == state.StateUnknown {
			t.Errorf("invalid state must not silently become UNKNOWN")
		}
	})
}

func TestCreateMonitor(t *testing.T) {
	repo := postgres.New(testDB)
	ctx := context.Background()

	t.Run("Monitor and UNKNOWN state are both created", func(t *testing.T) {
		cleanTables(t)
		id := "77777777-7777-7777-7777-777777777777"
		m := monitor.Monitor{
			ID:                  id,
			Name:                "Prod Web",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://prod.service.com",
			Method:              "POST",
			ExpectedStatusRange: "200-204",
		}

		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		// Verify monitors row
		var monitorCount int
		err := testDB.QueryRow("SELECT COUNT(*) FROM monitors WHERE id = $1", id).Scan(&monitorCount)
		if err != nil || monitorCount != 1 {
			t.Fatalf("expected 1 monitor row, got count=%d, err=%v", monitorCount, err)
		}

		// Verify monitor_states row
		var rawState string
		err = testDB.QueryRow("SELECT state FROM monitor_states WHERE monitor_id = $1", id).Scan(&rawState)
		if err != nil {
			t.Fatalf("failed to query monitor_states: %v", err)
		}
		if rawState != "UNKNOWN" {
			t.Errorf("expected initial state 'UNKNOWN', got %q", rawState)
		}
	})

	t.Run("Duplicate monitor ID fails", func(t *testing.T) {
		cleanTables(t)
		id := "88888888-8888-8888-8888-888888888888"
		m := monitor.Monitor{ID: id, Name: "M8", Kind: monitor.KindHTTP, TargetURL: "https://ex.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("initial CreateMonitor failed: %v", err)
		}

		// Second creation with same ID must fail
		err := repo.CreateMonitor(ctx, m)
		if err == nil {
			t.Fatal("expected error on duplicate ID, got nil")
		}
	})

	t.Run("If monitor_states insertion fails, the monitor insertion is rolled back", func(t *testing.T) {
		cleanTables(t)
		failingID := "deadbeef-0000-0000-0000-000000000001"

		// Create a temporary trigger on monitor_states that fails when inserting failingID
		_, err := testDB.Exec(`
			CREATE OR REPLACE FUNCTION test_fail_monitor_state_trigger() RETURNS TRIGGER AS $$
			BEGIN
				IF NEW.monitor_id = 'deadbeef-0000-0000-0000-000000000001'::uuid THEN
					RAISE EXCEPTION 'simulated monitor_states failure';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			DROP TRIGGER IF EXISTS trg_test_fail_state ON monitor_states;
			CREATE TRIGGER trg_test_fail_state
			BEFORE INSERT ON monitor_states
			FOR EACH ROW EXECUTE FUNCTION test_fail_monitor_state_trigger();
		`)
		if err != nil {
			t.Fatalf("setup trigger failed: %v", err)
		}
		defer func() {
			_, _ = testDB.Exec("DROP TRIGGER IF EXISTS trg_test_fail_state ON monitor_states;")
			_, _ = testDB.Exec("DROP FUNCTION IF EXISTS test_fail_monitor_state_trigger();")
		}()

		m := monitor.Monitor{
			ID:        failingID,
			Name:      "Failing Monitor",
			Kind:      monitor.KindHTTP,
			TargetURL: "https://fail.example.com",
		}

		err = repo.CreateMonitor(ctx, m)
		if err == nil {
			t.Fatal("expected CreateMonitor to fail due to trigger, got nil")
		}

		// Verify rollback: monitor row must NOT exist in the database
		var monitorCount int
		err = testDB.QueryRow("SELECT COUNT(*) FROM monitors WHERE id = $1", failingID).Scan(&monitorCount)
		if err != nil {
			t.Fatalf("query monitor count failed: %v", err)
		}
		if monitorCount != 0 {
			t.Errorf("expected monitor insertion to be rolled back, found %d rows in monitors", monitorCount)
		}

		var stateCount int
		err = testDB.QueryRow("SELECT COUNT(*) FROM monitor_states WHERE monitor_id = $1", failingID).Scan(&stateCount)
		if err != nil {
			t.Fatalf("query state count failed: %v", err)
		}
		if stateCount != 0 {
			t.Errorf("found %d rows in monitor_states, expected 0", stateCount)
		}
	})
}

func TestSaveCycle(t *testing.T) {
	repo := postgres.New(testDB)
	ctx := context.Background()

	t.Run("Successful cycle persists both check_results row and updated monitor state", func(t *testing.T) {
		cleanTables(t)
		id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
		m := monitor.Monitor{ID: id, Name: "A1", Kind: monitor.KindHTTP, TargetURL: "https://a.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		checkedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
		result := checker.CheckResult{
			MonitorID:    id,
			OK:           true,
			StatusCode:   200,
			Latency:      75 * time.Millisecond,
			AttemptCount: 1,
			CheckedAt:    checkedAt,
		}

		if err := repo.SaveCycle(ctx, id, result, state.StateHealthy); err != nil {
			t.Fatalf("SaveCycle failed: %v", err)
		}

		// Verify check_results row
		var (
			resID        int64
			ok           bool
			statusCode   sql.NullInt64
			latencyMs    int64
			attemptCount int
			resCheckedAt time.Time
		)
		err := testDB.QueryRow(`
			SELECT id, ok, status_code, latency_ms, attempt_count, checked_at
			FROM check_results
			WHERE monitor_id = $1
		`, id).Scan(&resID, &ok, &statusCode, &latencyMs, &attemptCount, &resCheckedAt)
		if err != nil {
			t.Fatalf("query check_results failed: %v", err)
		}

		if !ok {
			t.Errorf("expected ok=true, got %v", ok)
		}
		if !statusCode.Valid || statusCode.Int64 != 200 {
			t.Errorf("expected status_code=200, got %v", statusCode)
		}
		if latencyMs != 75 {
			t.Errorf("expected latency_ms=75, got %d", latencyMs)
		}
		if attemptCount != 1 {
			t.Errorf("expected attempt_count=1, got %d", attemptCount)
		}

		// Verify updated state
		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateHealthy {
			t.Errorf("expected state HEALTHY, got %q", st)
		}
	})

	t.Run("Correct fields and error classifications are persisted", func(t *testing.T) {
		cleanTables(t)
		id := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
		m := monitor.Monitor{ID: id, Name: "B1", Kind: monitor.KindHTTP, TargetURL: "https://b.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		checkedAt := time.Now().UTC().Truncate(time.Millisecond)
		result := checker.CheckResult{
			MonitorID:    id,
			OK:           false,
			StatusCode:   503,
			Latency:      450 * time.Millisecond,
			ErrorClass:   checker.ErrorClassStatus,
			ErrorDetail:  "Service Unavailable",
			AttemptCount: 3,
			CheckedAt:    checkedAt,
		}

		if err := repo.SaveCycle(ctx, id, result, state.StateUnhealthy); err != nil {
			t.Fatalf("SaveCycle failed: %v", err)
		}

		var (
			errorClass  sql.NullString
			errorDetail sql.NullString
		)
		err := testDB.QueryRow(`
			SELECT error_class, error_detail
			FROM check_results
			WHERE monitor_id = $1
		`, id).Scan(&errorClass, &errorDetail)
		if err != nil {
			t.Fatalf("query check_results failed: %v", err)
		}

		if !errorClass.Valid || errorClass.String != string(checker.ErrorClassStatus) {
			t.Errorf("expected error_class %q, got %v", checker.ErrorClassStatus, errorClass)
		}
		if !errorDetail.Valid || errorDetail.String != "Service Unavailable" {
			t.Errorf("expected error_detail 'Service Unavailable', got %v", errorDetail)
		}
	})

	t.Run("NULL optional result fields are handled correctly", func(t *testing.T) {
		cleanTables(t)
		id := "cccccccc-cccc-cccc-cccc-cccccccccccc"
		m := monitor.Monitor{ID: id, Name: "C1", Kind: monitor.KindTCP, TargetURL: "127.0.0.1:9999"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		// TCP check with StatusCode 0, ErrorClass None, ErrorDetail empty
		result := checker.CheckResult{
			MonitorID:    id,
			OK:           true,
			StatusCode:   0, // Absent HTTP status code
			Latency:      12 * time.Millisecond,
			ErrorClass:   checker.ErrorClassNone,
			ErrorDetail:  "",
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		}

		if err := repo.SaveCycle(ctx, id, result, state.StateHealthy); err != nil {
			t.Fatalf("SaveCycle failed: %v", err)
		}

		var (
			statusCode  sql.NullInt64
			errorClass  sql.NullString
			errorDetail sql.NullString
		)
		err := testDB.QueryRow(`
			SELECT status_code, error_class, error_detail
			FROM check_results
			WHERE monitor_id = $1
		`, id).Scan(&statusCode, &errorClass, &errorDetail)
		if err != nil {
			t.Fatalf("query check_results failed: %v", err)
		}

		if statusCode.Valid {
			t.Errorf("expected NULL status_code, got %d", statusCode.Int64)
		}
		if errorClass.Valid {
			t.Errorf("expected NULL error_class, got %q", errorClass.String)
		}
		if errorDetail.Valid {
			t.Errorf("expected NULL error_detail, got %q", errorDetail.String)
		}
	})

	t.Run("If state update affects zero rows, the transaction rolls back and no check result remains", func(t *testing.T) {
		cleanTables(t)
		id := "dddddddd-dddd-dddd-dddd-dddddddddddd"
		m := monitor.Monitor{ID: id, Name: "D1", Kind: monitor.KindHTTP, TargetURL: "https://d.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		// Manually delete the monitor_states row to simulate orphan state
		if _, err := testDB.Exec("DELETE FROM monitor_states WHERE monitor_id = $1", id); err != nil {
			t.Fatalf("delete state failed: %v", err)
		}

		result := checker.CheckResult{
			MonitorID:    id,
			OK:           true,
			StatusCode:   200,
			Latency:      50 * time.Millisecond,
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		}

		err := repo.SaveCycle(ctx, id, result, state.StateHealthy)
		if err == nil {
			t.Fatal("expected SaveCycle to return error when state row does not exist, got nil")
		}

		// Verify transaction rollback: check_results row must NOT exist
		var checkCount int
		err = testDB.QueryRow("SELECT COUNT(*) FROM check_results WHERE monitor_id = $1", id).Scan(&checkCount)
		if err != nil {
			t.Fatalf("query check_results count failed: %v", err)
		}
		if checkCount != 0 {
			t.Errorf("expected rollback of check_results, found %d rows", checkCount)
		}
	})

	t.Run("If check-result insertion fails, no state change occurs", func(t *testing.T) {
		cleanTables(t)
		id := "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
		m := monitor.Monitor{ID: id, Name: "E1", Kind: monitor.KindHTTP, TargetURL: "https://e.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		// Initial state is UNKNOWN. Now attempt to save invalid result: negative latency violates CHECK constraint
		invalidResult := checker.CheckResult{
			MonitorID:    id,
			OK:           false,
			StatusCode:   0,
			Latency:      -10 * time.Millisecond, // Invalid!
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		}

		err := repo.SaveCycle(ctx, id, invalidResult, state.StateHealthy)
		if err == nil {
			t.Fatal("expected error due to negative latency constraint, got nil")
		}

		// Verify state was NOT updated to HEALTHY
		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateUnknown {
			t.Errorf("state should remain UNKNOWN, got %q", st)
		}
	})

	t.Run("Context cancellation is respected", func(t *testing.T) {
		cleanTables(t)
		id := "ffffffff-ffff-ffff-ffff-ffffffffffff"
		m := monitor.Monitor{ID: id, Name: "F1", Kind: monitor.KindHTTP, TargetURL: "https://f.com"}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		canceledCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		result := checker.CheckResult{
			MonitorID:    id,
			OK:           true,
			StatusCode:   200,
			Latency:      30 * time.Millisecond,
			AttemptCount: 1,
			CheckedAt:    time.Now().UTC(),
		}

		err := repo.SaveCycle(canceledCtx, id, result, state.StateHealthy)
		if err == nil {
			t.Fatal("expected error with canceled context, got nil")
		}

		// Verify state was NOT modified
		st, err := repo.GetState(ctx, id)
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}
		if st != state.StateUnknown {
			t.Errorf("expected state to remain UNKNOWN, got %q", st)
		}
	})
}

func TestConcurrentRepositoryUsage(t *testing.T) {
	cleanTables(t)
	repo := postgres.New(testDB)
	ctx := context.Background()

	const numWorkers = 10
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			mID := fmt.Sprintf("99999999-9999-9999-9999-%012d", workerID)
			m := monitor.Monitor{
				ID:        mID,
				Name:      fmt.Sprintf("Worker-%d", workerID),
				Kind:      monitor.KindHTTP,
				TargetURL: fmt.Sprintf("https://worker-%d.example.com", workerID),
				Method:    "GET",
			}

			if err := repo.CreateMonitor(ctx, m); err != nil {
				t.Errorf("worker %d CreateMonitor failed: %v", workerID, err)
				return
			}

			st, err := repo.GetState(ctx, mID)
			if err != nil || st != state.StateUnknown {
				t.Errorf("worker %d GetState initial failed: st=%v, err=%v", workerID, st, err)
				return
			}

			res := checker.CheckResult{
				MonitorID:    mID,
				OK:           true,
				StatusCode:   200,
				Latency:      25 * time.Millisecond,
				AttemptCount: 1,
				CheckedAt:    time.Now().UTC(),
			}

			if err := repo.SaveCycle(ctx, mID, res, state.StateHealthy); err != nil {
				t.Errorf("worker %d SaveCycle failed: %v", workerID, err)
				return
			}

			got, err := repo.GetMonitor(ctx, mID)
			if err != nil || got.ID != mID {
				t.Errorf("worker %d GetMonitor failed: %v", workerID, err)
				return
			}

			st, err = repo.GetState(ctx, mID)
			if err != nil || st != state.StateHealthy {
				t.Errorf("worker %d GetState after cycle failed: st=%v, err=%v", workerID, st, err)
				return
			}
		}(i)
	}

	wg.Wait()
}
