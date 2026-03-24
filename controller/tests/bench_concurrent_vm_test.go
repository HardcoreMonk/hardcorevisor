package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/api"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/backup"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/image"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/snapshot"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/task"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/template"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

func setupBenchServer(b *testing.B) (*httptest.Server, func()) {
	b.Helper()
	core := ffi.NewMockVMCore()
	if err := core.Init(); err != nil {
		b.Fatal(err)
	}

	rustVMM := compute.NewRustVMMBackend(core)
	qemuBackend := compute.NewQEMUBackend(&compute.QEMUConfig{Emulated: true})
	lxcBackend := compute.NewLXCBackend(&compute.LXCBackendConfig{Emulated: true})
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(rustVMM)
	selector.Register(qemuBackend)
	selector.Register(lxcBackend)
	computeSvc := compute.NewComputeService(selector, rustVMM)

	storageSvc := storage.NewService()

	svc := &api.Services{
		Compute:    computeSvc,
		Storage:    storageSvc,
		Network:    network.NewService(),
		Peripheral: peripheral.NewService(),
		HA:         ha.NewService(),
		Backup:     backup.NewService(storageSvc),
		Template:   template.NewService(),
		Snapshot:   snapshot.NewService(),
		Image:      image.NewService("/tmp/hcv-bench-images"),
		LXC:        lxcBackend,
		Task:       task.NewTaskService(),
		Version: api.VersionInfo{
			Version:   "bench",
			GitCommit: "bench123",
			BuildDate: time.Now().Format(time.RFC3339),
			VMCore:    core.Version(),
		},
	}
	router := api.NewRouter(svc)
	server := httptest.NewServer(router)

	cleanup := func() {
		server.Close()
		core.Shutdown()
	}
	return server, cleanup
}

// TestBench_ConcurrentVMCreation creates 50 VMs concurrently and measures
// total time and P99 latency through the full Services stack.
func TestBench_ConcurrentVMCreation(t *testing.T) {
	server, cleanup := setupBenchServer(&testing.B{})
	defer cleanup()

	const numVMs = 50
	var wg sync.WaitGroup
	latencies := make([]time.Duration, numVMs)
	errors := make([]error, numVMs)

	start := time.Now()

	for i := 0; i < numVMs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"name":"bench-vm-%d","vcpus":2,"memory_mb":1024}`, idx)
			reqStart := time.Now()
			resp, err := http.Post(
				server.URL+"/api/v1/vms",
				"application/json",
				bytes.NewBufferString(body),
			)
			latencies[idx] = time.Since(reqStart)
			if err != nil {
				errors[idx] = err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				errors[idx] = fmt.Errorf("unexpected status %d for vm-%d", resp.StatusCode, idx)
			}
		}(i)
	}

	wg.Wait()
	totalTime := time.Since(start)

	// Count errors
	errorCount := 0
	for _, err := range errors {
		if err != nil {
			errorCount++
			t.Logf("  error: %v", err)
		}
	}

	// Calculate P50, P95, P99
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[numVMs*50/100]
	p95 := latencies[numVMs*95/100]
	p99 := latencies[numVMs*99/100]
	minLat := latencies[0]
	maxLat := latencies[numVMs-1]

	// Verify all VMs exist via list (paginated response)
	resp, err := http.Get(server.URL + "/api/v1/vms?limit=100")
	if err != nil {
		t.Fatalf("list VMs failed: %v", err)
	}
	defer resp.Body.Close()
	var listResp struct {
		Data       []json.RawMessage `json:"data"`
		TotalCount int               `json:"total_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode VMs failed: %v", err)
	}
	vmCount := listResp.TotalCount

	t.Logf("=== Concurrent VM Creation Benchmark ===")
	t.Logf("  VMs created:  %d", numVMs)
	t.Logf("  Errors:       %d", errorCount)
	t.Logf("  Total time:   %v", totalTime)
	t.Logf("  Throughput:   %.1f VMs/sec", float64(numVMs)/totalTime.Seconds())
	t.Logf("  Latency min:  %v", minLat)
	t.Logf("  Latency P50:  %v", p50)
	t.Logf("  Latency P95:  %v", p95)
	t.Logf("  Latency P99:  %v", maxLat)
	t.Logf("  Latency max:  %v", maxLat)
	t.Logf("  VMs in list:  %d", vmCount)

	if errorCount > 0 {
		t.Errorf("expected 0 errors, got %d", errorCount)
	}
	if vmCount < numVMs-errorCount {
		t.Errorf("expected at least %d VMs in list, got %d", numVMs-errorCount, vmCount)
	}

	// Also run a quick sequential baseline for comparison
	seqStart := time.Now()
	for i := 0; i < 10; i++ {
		body := fmt.Sprintf(`{"name":"seq-vm-%d","vcpus":1,"memory_mb":512}`, i)
		resp, err := http.Post(
			server.URL+"/api/v1/vms",
			"application/json",
			bytes.NewBufferString(body),
		)
		if err != nil {
			t.Errorf("sequential create %d failed: %v", i, err)
			continue
		}
		resp.Body.Close()
	}
	seqTime := time.Since(seqStart)
	t.Logf("  Sequential 10 VMs: %v (%.1f VMs/sec)", seqTime, 10.0/seqTime.Seconds())
	_ = p50
	_ = p95
	_ = p99
	_ = minLat
}
