package benchmark_test

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type LatencyStats struct {
	Count      int
	Duration   time.Duration
	Throughput float64
	Min        time.Duration
	Avg        time.Duration
	P50        time.Duration
	P90        time.Duration
	P95        time.Duration
	P99        time.Duration
	Max        time.Duration
}

func CalculateStats(durations []time.Duration, totalWallDuration time.Duration) LatencyStats {
	if len(durations) == 0 {
		return LatencyStats{}
	}
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	p := func(percentile float64) time.Duration {
		idx := int(float64(len(sorted)-1) * percentile)
		return sorted[idx]
	}

	throughput := 0.0
	if totalWallDuration > 0 {
		throughput = float64(len(durations)) / totalWallDuration.Seconds()
	}

	return LatencyStats{
		Count:      len(sorted),
		Duration:   totalWallDuration,
		Throughput: throughput,
		Min:        sorted[0],
		Avg:        total / time.Duration(len(sorted)),
		P50:        p(0.50),
		P90:        p(0.90),
		P95:        p(0.95),
		P99:        p(0.99),
		Max:        sorted[len(sorted)-1],
	}
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func StartEphemeralPostgres() (*sql.DB, string, func(), error) {
	tempDir, err := os.MkdirTemp("", "watchdog_pg_bench_*")
	if err != nil {
		return nil, "", nil, fmt.Errorf("create temp dir: %w", err)
	}

	dataDir := filepath.Join(tempDir, "data")
	port, err := getFreePort()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("find free port: %w", err)
	}

	initCmd := exec.Command("initdb", "-D", dataDir, "-U", "postgres", "--auth=trust", "-A", "trust")
	if out, err := initCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("initdb failed: %s: %w", string(out), err)
	}

	logFile := filepath.Join(tempDir, "postgres.log")
	pgOptions := fmt.Sprintf("-k '' -h 127.0.0.1 -p %d", port)
	startCmd := exec.Command("pg_ctl", "-D", dataDir, "-o", pgOptions, "-l", logFile, "start")
	if out, err := startCmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("pg_ctl start failed: %s: %w", string(out), err)
	}

	createDbCmd := exec.Command("createdb", "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", "postgres", "watchdog_bench")
	if out, err := createDbCmd.CombinedOutput(); err != nil {
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("createdb failed: %s: %w", string(out), err)
	}

	connStr := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/watchdog_bench?sslmode=disable", port)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("open db: %w", err)
	}

	// Verify ping
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
		return nil, "", nil, fmt.Errorf("ping db: %w", err)
	}

	// Apply migrations
	migrations := []string{
		"../../migrations/001_initial_schema.up.sql",
		"../../migrations/002_monitor_scheduling_fields.up.sql",
	}
	for _, mFile := range migrations {
		sqlBytes, err := os.ReadFile(mFile)
		if err != nil {
			_ = db.Close()
			_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
			_ = os.RemoveAll(tempDir)
			return nil, "", nil, fmt.Errorf("read migration %s: %w", mFile, err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			_ = db.Close()
			_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
			_ = os.RemoveAll(tempDir)
			return nil, "", nil, fmt.Errorf("exec migration %s: %w", mFile, err)
		}
	}

	cleanup := func() {
		_ = db.Close()
		_ = exec.Command("pg_ctl", "-D", dataDir, "stop", "-m", "immediate").Run()
		_ = os.RemoveAll(tempDir)
	}

	return db, connStr, cleanup, nil
}

// StartLocalHTTPServer starts a fast or delayed HTTP server.
func StartLocalHTTPServer(delay time.Duration, statusCode int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
}

// StartLocalTCPListener starts an echo/immediate-accept TCP listener on localhost.
func StartLocalTCPListener() (net.Listener, string, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := l.Addr().String()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 128)
				_, _ = c.Read(buf)
			}(conn)
		}
	}()

	cleanup := func() {
		close(stopCh)
		_ = l.Close()
		wg.Wait()
	}

	return l, addr, cleanup
}

// StartHangingTCPListener accepts connections but never responds or closes until stopped.
func StartHangingTCPListener() (net.Listener, string, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	addr := l.Addr().String()

	stopCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var conns []net.Conn
		for {
			conn, err := l.Accept()
			if err != nil {
				for _, c := range conns {
					_ = c.Close()
				}
				return
			}
			conns = append(conns, conn)
		}
	}()

	cleanup := func() {
		close(stopCh)
		_ = l.Close()
		wg.Wait()
	}

	return l, addr, cleanup
}
