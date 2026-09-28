package benchmark_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chavaliadi/watchdog/internal/api"
	"github.com/chavaliadi/watchdog/internal/checker"
	"github.com/chavaliadi/watchdog/internal/monitor"
	"github.com/chavaliadi/watchdog/internal/persistence/postgres"
	"github.com/chavaliadi/watchdog/internal/retry"
	"github.com/chavaliadi/watchdog/internal/scheduler"
	"github.com/chavaliadi/watchdog/internal/service"
	"github.com/chavaliadi/watchdog/internal/state"
	"github.com/chavaliadi/watchdog/internal/telemetry"
	"github.com/chavaliadi/watchdog/internal/worker"
)

func TestFrontend_NPlusOne_Simulation(t *testing.T) {
	db, _, cleanup, err := StartEphemeralPostgres()
	if err != nil {
		t.Fatalf("start ephemeral postgres: %v", err)
	}
	defer cleanup()

	repo := postgres.New(db)
	orch := scheduler.NewOrchestrator(nil, nil, retry.Config{}, repo)
	pool, _ := worker.NewPool(worker.Config{MaxConcurrency: 5}, orch)
	pool.Start(context.Background())
	defer func() {
		pool.Stop()
		pool.Wait()
	}()

	multiSched, _ := scheduler.NewMultiScheduler(repo, pool)
	_ = multiSched.Start(context.Background())
	defer func() {
		multiSched.Stop()
		multiSched.Wait()
	}()

	monitorSvc := service.NewMonitorService(repo, multiSched)
	metrics := telemetry.NewMetrics()
	handlers := api.NewHandlers(
		monitorSvc,
		api.WithHealthChecks(db, multiSched, nil),
		api.WithMetrics(metrics.Handler()),
		api.WithRecorder(metrics),
	)

	apiServer := api.NewServer(api.Config{}, handlers)
	server := httptest.NewServer(apiServer.Handler())
	defer server.Close()

	// Client configured with transport similar to browser or high-concurrency client
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 1000,
			MaxConnsPerHost:     1000,
		},
		Timeout: 30 * time.Second,
	}

	scales := []int{10, 50, 100, 250, 500}

	fmt.Println("\n======================= FRONTEND N+1 DASHBOARD LOAD SIMULATION =======================")
	fmt.Printf("%-10s | %-12s | %-12s | %-12s | %-12s | %-12s | %-12s | %-10s\n",
		"Monitors", "Total Req", "Dashboard Time", "Avg Status Lat", "P50 Status Lat", "P95 Status Lat", "P99 Status Lat", "DB Conns")
	fmt.Println("-------------------------------------------------------------------------------------------------------")

	ctx := context.Background()

	for _, count := range scales {
		// Clean and seed
		_, _ = db.Exec("TRUNCATE monitors CASCADE")

		for i := 0; i < count; i++ {
			id := fmt.Sprintf("20000000-0000-0000-0000-%012d", i+1)
			m := monitor.Monitor{
				ID:                  id,
				Name:                fmt.Sprintf("Dashboard Monitor %d", i+1),
				Kind:                monitor.KindHTTP,
				TargetURL:           "http://127.0.0.1:8080/test",
				Method:              "GET",
				ExpectedStatusRange: "200-299",
				Interval:            60 * time.Second,
				Timeout:             5 * time.Second,
				Enabled:             true,
			}
			_ = repo.CreateMonitor(ctx, m)
			_ = repo.SaveCycle(ctx, id, checker.CheckResult{
				MonitorID:    id,
				CheckedAt:    time.Now().UTC(),
				OK:           true,
				StatusCode:   200,
				Latency:      12 * time.Millisecond,
				AttemptCount: 1,
			}, state.StateHealthy)
		}

		// Simulate Dashboard Load:
		// 1. GET /monitors
		dashboardStart := time.Now()
		resp, err := client.Get(server.URL + "/monitors")
		if err != nil {
			t.Fatalf("GET /monitors failed: %v", err)
		}
		var listResp []struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&listResp)
		_ = resp.Body.Close()

		if len(listResp) != count {
			t.Fatalf("expected %d monitors, got %d", count, len(listResp))
		}

		// 2. Concurrently fetch status for each monitor
		var wg sync.WaitGroup
		statusDurations := make([]time.Duration, count)

		for idx, m := range listResp {
			wg.Add(1)
			go func(i int, monID string) {
				defer wg.Done()
				sStart := time.Now()
				sResp, sErr := client.Get(fmt.Sprintf("%s/monitors/%s/status", server.URL, monID))
				statusDurations[i] = time.Since(sStart)
				if sErr != nil || sResp.StatusCode != http.StatusOK {
					return
				}
				_ = sResp.Body.Close()
			}(idx, m.ID)
		}
		wg.Wait()
		dashboardTotalTime := time.Since(dashboardStart)

		stats := CalculateStats(statusDurations, dashboardTotalTime)
		dbStats := db.Stats()

		fmt.Printf("%-10d | %-12d | %-14v | %-14v | %-14v | %-14v | %-14v | %-10d\n",
			count, count+1, dashboardTotalTime, stats.Avg, stats.P50, stats.P95, stats.P99, dbStats.OpenConnections)
	}
	fmt.Println("=======================================================================================================")
}
