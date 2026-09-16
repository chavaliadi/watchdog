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

	// Apply schema migrations (001 and 002)
	migrationFiles := []string{
		"001_initial_schema.up.sql",
		"002_monitor_scheduling_fields.up.sql",
	}
	for _, migrationFile := range migrationFiles {
		schemaPath := filepath.Join("..", "..", "..", "migrations", migrationFile)
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
			log.Fatalf("failed to execute migration %q: %v", migrationFile, err)
		}
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

	t.Run("Interval, Timeout, and Enabled round-trip correctly", func(t *testing.T) {
		cleanTables(t)

		id := "12345678-1234-1234-1234-123456789012"
		m := monitor.Monitor{
			ID:                  id,
			Name:                "Custom Timing",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://example.com/timing",
			Method:              "GET",
			ExpectedStatusRange: "200-299",
			Interval:            15 * time.Second,
			Timeout:             3500 * time.Millisecond,
			Enabled:             true,
		}
		if err := repo.CreateMonitor(ctx, m); err != nil {
			t.Fatalf("CreateMonitor failed: %v", err)
		}

		got, err := repo.GetMonitor(ctx, id)
		if err != nil {
			t.Fatalf("GetMonitor failed: %v", err)
		}

		if got.Interval != 15*time.Second {
			t.Errorf("Interval mismatch: got %v, want %v", got.Interval, 15*time.Second)
		}
		if got.Timeout != 3500*time.Millisecond {
			t.Errorf("Timeout mismatch: got %v, want %v", got.Timeout, 3500*time.Millisecond)
		}
		if got.Enabled != true {
			t.Errorf("Enabled mismatch: got %v, want true", got.Enabled)
		}

		// Also verify disabled monitor round-trips
		idDisabled := "87654321-4321-4321-4321-210987654321"
		mDisabled := monitor.Monitor{
			ID:        idDisabled,
			Name:      "Disabled Timing",
			Kind:      monitor.KindHTTP,
			TargetURL: "https://example.com/disabled",
			Interval:  45 * time.Second,
			Timeout:   2 * time.Second,
			Enabled:   false,
		}
		if err := repo.CreateMonitor(ctx, mDisabled); err != nil {
			t.Fatalf("CreateMonitor disabled failed: %v", err)
		}

		gotDisabled, err := repo.GetMonitor(ctx, idDisabled)
		if err != nil {
			t.Fatalf("GetMonitor disabled failed: %v", err)
		}
		if gotDisabled.Enabled != false {
			t.Errorf("Enabled mismatch: got %v, want false", gotDisabled.Enabled)
		}
		if gotDisabled.Interval != 45*time.Second {
			t.Errorf("Interval mismatch: got %v, want %v", gotDisabled.Interval, 45*time.Second)
		}
	})
}

func TestListMonitors(t *testing.T) {
	repo := postgres.New(testDB)
	ctx := context.Background()

	t.Run("Empty table returns empty slice and nil error", func(t *testing.T) {
		cleanTables(t)

		list, err := repo.ListMonitors(ctx)
		if err != nil {
			t.Fatalf("ListMonitors failed: %v", err)
		}
		if list == nil {
			t.Fatal("expected non-nil empty slice, got nil")
		}
		if len(list) != 0 {
			t.Errorf("expected 0 monitors, got %d", len(list))
		}
	})

	t.Run("Multiple monitors returned with deterministic ordering and full field hydration", func(t *testing.T) {
		cleanTables(t)

		t1 := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
		t2 := time.Date(2026, 9, 16, 10, 1, 0, 0, time.UTC)
		t3 := time.Date(2026, 9, 16, 10, 2, 0, 0, time.UTC)

		m1 := monitor.Monitor{
			ID:                  "11111111-1111-1111-1111-111111111111",
			Name:                "Alpha Monitor",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://alpha.example.com",
			Method:              "GET",
			ExpectedStatusRange: "200-299",
			Interval:            30 * time.Second,
			Timeout:             4 * time.Second,
			Enabled:             true,
			CreatedAt:           t1,
		}
		m2 := monitor.Monitor{
			ID:        "22222222-2222-2222-2222-222222222222",
			Name:      "Beta Monitor",
			Kind:      monitor.KindTCP,
			TargetURL: "127.0.0.1:5432",
			Interval:  10 * time.Second,
			Timeout:   1 * time.Second,
			Enabled:   false,
			CreatedAt: t2,
		}
		m3 := monitor.Monitor{
			ID:                  "33333333-3333-3333-3333-333333333333",
			Name:                "Gamma Monitor",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://gamma.example.com/api",
			Method:              "POST",
			ExpectedStatusRange: "201",
			Interval:            60 * time.Second,
			Timeout:             5 * time.Second,
			Enabled:             true,
			CreatedAt:           t3,
		}

		// Insert out of order (m2, m3, m1)
		for _, m := range []monitor.Monitor{m2, m3, m1} {
			if err := repo.CreateMonitor(ctx, m); err != nil {
				t.Fatalf("CreateMonitor %s failed: %v", m.ID, err)
			}
		}

		list, err := repo.ListMonitors(ctx)
		if err != nil {
			t.Fatalf("ListMonitors failed: %v", err)
		}

		if len(list) != 3 {
			t.Fatalf("expected 3 monitors, got %d", len(list))
		}

		// Verify deterministic ordering by created_at ASC (m1, m2, m3)
		expectedIDs := []string{m1.ID, m2.ID, m3.ID}
		for i, expectedID := range expectedIDs {
			if list[i].ID != expectedID {
				t.Errorf("index %d ID mismatch: got %q, want %q", i, list[i].ID, expectedID)
			}
		}

		// Verify full hydration of m1
		got1 := list[0]
		if got1.Name != m1.Name || got1.Kind != m1.Kind || got1.TargetURL != m1.TargetURL ||
			got1.Method != m1.Method || got1.ExpectedStatusRange != m1.ExpectedStatusRange ||
			got1.Interval != m1.Interval || got1.Timeout != m1.Timeout || got1.Enabled != m1.Enabled {
			t.Errorf("m1 hydration mismatch: got %+v, want %+v", got1, m1)
		}

		// Verify full hydration of m2 (TCP monitor with empty Method/ExpectedStatusRange and Enabled=false)
		got2 := list[1]
		if got2.Name != m2.Name || got2.Kind != m2.Kind || got2.TargetURL != m2.TargetURL ||
			got2.Method != "" || got2.ExpectedStatusRange != "" ||
			got2.Interval != m2.Interval || got2.Timeout != m2.Timeout || got2.Enabled != false {
			t.Errorf("m2 hydration mismatch: got %+v, want %+v", got2, m2)
		}
	})

	t.Run("Context cancellation returns error", func(t *testing.T) {
		cleanTables(t)

		cancCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := repo.ListMonitors(cancCtx)
		if err == nil {
			t.Fatal("expected error with canceled context, got nil")
		}
	})

	t.Run("Database error returns wrapped error", func(t *testing.T) {
		badDB, err := sql.Open("pgx", "postgres://invalid:invalid@127.0.0.1:1/invalid?sslmode=disable")
		if err != nil {
			t.Fatalf("open bad db failed: %v", err)
		}
		_ = badDB.Close()

		badRepo := postgres.New(badDB)
		_, err = badRepo.ListMonitors(ctx)
		if err == nil {
			t.Fatal("expected error on closed db, got nil")
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

func TestUpdateMonitor(t *testing.T) {
	cleanTables(t)
	repo := postgres.New(testDB)
	ctx := context.Background()

	mID := "11111111-2222-3333-4444-555555555555"
	m := monitor.Monitor{
		ID:                  mID,
		Name:                "Original Name",
		Kind:                monitor.KindHTTP,
		TargetURL:           "https://original.example.com",
		Method:              "GET",
		ExpectedStatusRange: "200",
		Interval:            30 * time.Second,
		Timeout:             5 * time.Second,
		Enabled:             true,
	}

	if err := repo.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}

	t.Run("successful update", func(t *testing.T) {
		updated := monitor.Monitor{
			ID:                  mID,
			Name:                "Updated Name",
			Kind:                monitor.KindHTTP,
			TargetURL:           "https://updated.example.com",
			Method:              "POST",
			ExpectedStatusRange: "200-204",
			Interval:            10 * time.Second,
			Timeout:             2 * time.Second,
			Enabled:             false,
		}

		if err := repo.UpdateMonitor(ctx, updated); err != nil {
			t.Fatalf("UpdateMonitor failed: %v", err)
		}

		got, err := repo.GetMonitor(ctx, mID)
		if err != nil {
			t.Fatalf("GetMonitor failed: %v", err)
		}

		if got.Name != "Updated Name" {
			t.Errorf("expected Name %q, got %q", "Updated Name", got.Name)
		}
		if got.TargetURL != "https://updated.example.com" {
			t.Errorf("expected TargetURL %q, got %q", "https://updated.example.com", got.TargetURL)
		}
		if got.Method != "POST" {
			t.Errorf("expected Method %q, got %q", "POST", got.Method)
		}
		if got.ExpectedStatusRange != "200-204" {
			t.Errorf("expected ExpectedStatusRange %q, got %q", "200-204", got.ExpectedStatusRange)
		}
		if got.Interval != 10*time.Second {
			t.Errorf("expected Interval %v, got %v", 10*time.Second, got.Interval)
		}
		if got.Timeout != 2*time.Second {
			t.Errorf("expected Timeout %v, got %v", 2*time.Second, got.Timeout)
		}
		if got.Enabled != false {
			t.Errorf("expected Enabled false, got true")
		}
	})

	t.Run("missing monitor returns ErrNoRows", func(t *testing.T) {
		missing := monitor.Monitor{
			ID:        "00000000-0000-0000-0000-000000000000",
			Name:      "Missing",
			TargetURL: "https://missing.example.com",
		}
		err := repo.UpdateMonitor(ctx, missing)
		if err == nil {
			t.Fatal("expected error updating missing monitor, got nil")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected errors.Is(err, sql.ErrNoRows), got: %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := repo.UpdateMonitor(canceledCtx, m)
		if err == nil {
			t.Fatal("expected error with canceled context, got nil")
		}
	})
}

func TestDeleteMonitor(t *testing.T) {
	cleanTables(t)
	repo := postgres.New(testDB)
	ctx := context.Background()

	mID := "22222222-3333-4444-5555-666666666666"
	m := monitor.Monitor{
		ID:        mID,
		Name:      "To Be Deleted",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://delete.example.com",
		Interval:  10 * time.Second,
		Timeout:   2 * time.Second,
		Enabled:   true,
	}

	if err := repo.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}

	// Add a check result to test cascade delete
	cycleRes := checker.CheckResult{
		MonitorID:    mID,
		OK:           true,
		StatusCode:   200,
		Latency:      100 * time.Millisecond,
		AttemptCount: 1,
		CheckedAt:    time.Now().UTC(),
	}
	if err := repo.SaveCycle(ctx, mID, cycleRes, state.StateHealthy); err != nil {
		t.Fatalf("SaveCycle failed: %v", err)
	}

	t.Run("successful delete with cascade", func(t *testing.T) {
		if err := repo.DeleteMonitor(ctx, mID); err != nil {
			t.Fatalf("DeleteMonitor failed: %v", err)
		}

		// Verify monitor is gone
		_, err := repo.GetMonitor(ctx, mID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected ErrNoRows getting deleted monitor, got %v", err)
		}

		// Verify monitor state is cascade deleted
		_, err = repo.GetState(ctx, mID)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected ErrNoRows getting deleted monitor state, got %v", err)
		}

		// Verify check results are cascade deleted
		history, err := repo.ListCheckResults(ctx, mID, 10)
		if err != nil {
			t.Fatalf("ListCheckResults failed: %v", err)
		}
		if len(history) != 0 {
			t.Fatalf("expected 0 check results after cascade delete, got %d", len(history))
		}
	})

	t.Run("delete missing monitor returns ErrNoRows", func(t *testing.T) {
		err := repo.DeleteMonitor(ctx, "00000000-0000-0000-0000-000000000000")
		if err == nil {
			t.Fatal("expected error deleting missing monitor, got nil")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("expected errors.Is(err, sql.ErrNoRows), got: %v", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()

		err := repo.DeleteMonitor(canceledCtx, mID)
		if err == nil {
			t.Fatal("expected error with canceled context, got nil")
		}
	})
}

func TestListCheckResults(t *testing.T) {
	cleanTables(t)
	repo := postgres.New(testDB)
	ctx := context.Background()

	mID := "33333333-4444-5555-6666-777777777777"
	m := monitor.Monitor{
		ID:        mID,
		Name:      "History Test",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://history.example.com",
		Interval:  10 * time.Second,
		Timeout:   2 * time.Second,
		Enabled:   true,
	}

	if err := repo.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}

	t.Run("empty history returns empty slice", func(t *testing.T) {
		results, err := repo.ListCheckResults(ctx, mID, 10)
		if err != nil {
			t.Fatalf("ListCheckResults failed: %v", err)
		}
		if results == nil {
			t.Fatal("expected non-nil empty slice")
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 check results, got %d", len(results))
		}
	})

	t.Run("history ordering and fields", func(t *testing.T) {
		baseTime := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
		// Save 5 cycles with increasing timestamps
		for i := 1; i <= 5; i++ {
			res := checker.CheckResult{
				MonitorID:    mID,
				OK:           i%2 == 1,
				StatusCode:   200 + i,
				Latency:      time.Duration(i*10) * time.Millisecond,
				AttemptCount: i,
				CheckedAt:    baseTime.Add(time.Duration(i) * time.Minute),
			}
			st := state.StateHealthy
			if !res.OK {
				st = state.StateUnhealthy
				res.ErrorClass = checker.ErrorClassStatus
				res.ErrorDetail = fmt.Sprintf("status code %d", res.StatusCode)
			}
			if err := repo.SaveCycle(ctx, mID, res, st); err != nil {
				t.Fatalf("SaveCycle %d failed: %v", i, err)
			}
		}

		// Retrieve with limit 3 -> should get cycles 5, 4, 3 (most recent first)
		results, err := repo.ListCheckResults(ctx, mID, 3)
		if err != nil {
			t.Fatalf("ListCheckResults failed: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}

		if results[0].StatusCode != 205 {
			t.Errorf("expected first result to be cycle 5 (code 205), got %d", results[0].StatusCode)
		}
		if results[1].StatusCode != 204 {
			t.Errorf("expected second result to be cycle 4 (code 204), got %d", results[1].StatusCode)
		}
		if results[2].StatusCode != 203 {
			t.Errorf("expected third result to be cycle 3 (code 203), got %d", results[2].StatusCode)
		}
		if results[1].ErrorClass != checker.ErrorClassStatus {
			t.Errorf("expected error class status, got %q", results[1].ErrorClass)
		}
		if results[1].ErrorDetail != "status code 204" {
			t.Errorf("expected error detail 'status code 204', got %q", results[1].ErrorDetail)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()

		_, err := repo.ListCheckResults(canceledCtx, mID, 10)
		if err == nil {
			t.Fatal("expected error with canceled context, got nil")
		}
	})
}

func TestGetStateWithTimestamp(t *testing.T) {
	cleanTables(t)
	repo := postgres.New(testDB)
	ctx := context.Background()

	mID := "44444444-5555-6666-7777-888888888888"
	m := monitor.Monitor{
		ID:        mID,
		Name:      "Timestamp Test",
		Kind:      monitor.KindHTTP,
		TargetURL: "https://timestamp.example.com",
		Interval:  10 * time.Second,
		Timeout:   2 * time.Second,
		Enabled:   true,
	}

	if err := repo.CreateMonitor(ctx, m); err != nil {
		t.Fatalf("CreateMonitor failed: %v", err)
	}

	st, ts, err := repo.GetStateWithTimestamp(ctx, mID)
	if err != nil {
		t.Fatalf("GetStateWithTimestamp failed: %v", err)
	}
	if st != state.StateUnknown {
		t.Errorf("expected initial state UNKNOWN, got %q", st)
	}
	if ts.IsZero() {
		t.Error("expected non-zero initial updated_at timestamp")
	}

	// Update via SaveCycle
	checkedAt := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	cycleRes := checker.CheckResult{
		MonitorID:    mID,
		OK:           true,
		StatusCode:   200,
		Latency:      50 * time.Millisecond,
		AttemptCount: 1,
		CheckedAt:    checkedAt,
	}
	if err := repo.SaveCycle(ctx, mID, cycleRes, state.StateHealthy); err != nil {
		t.Fatalf("SaveCycle failed: %v", err)
	}

	st, ts, err = repo.GetStateWithTimestamp(ctx, mID)
	if err != nil {
		t.Fatalf("GetStateWithTimestamp after cycle failed: %v", err)
	}
	if st != state.StateHealthy {
		t.Errorf("expected state HEALTHY, got %q", st)
	}
	if ts.Sub(checkedAt).Abs() > time.Second {
		t.Errorf("expected updated_at close to %v, got %v", checkedAt, ts)
	}

	// Missing monitor
	_, _, err = repo.GetStateWithTimestamp(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows for missing monitor, got %v", err)
	}
}

