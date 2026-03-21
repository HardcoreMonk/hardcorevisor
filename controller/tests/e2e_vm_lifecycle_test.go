// Package tests provides end-to-end integration tests for HardCoreVisor.
// These tests spin up the full Go Controller with Mock FFI backend
// and exercise the REST API through real HTTP requests.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/api"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

// setupE2E creates a full test server with Mock VMCore backend.
func setupE2E(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	core := ffi.NewMockVMCore()
	if err := core.Init(); err != nil {
		t.Fatal(err)
	}

	rustVMM := compute.NewRustVMMBackend(core)
	qemuBackend := compute.NewQEMUBackend(&compute.QEMUConfig{Emulated: true})
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(rustVMM)
	selector.Register(qemuBackend)
	computeSvc := compute.NewComputeService(selector, rustVMM)

	svc := &api.Services{
		Compute:    computeSvc,
		Storage:    storage.NewService(),
		Network:    network.NewService(),
		Peripheral: peripheral.NewService(),
		HA:         ha.NewService(),
		Version: api.VersionInfo{
			Version:   "test-e2e",
			GitCommit: "abc123",
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

// ── E2E: Full VM Lifecycle ───────────────────────────────

func TestE2E_FullVMLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Healthcheck
	resp := httpGet(t, base+"/healthz")
	assertStatus(t, resp, 200)
	var health map[string]string
	decodeJSON(t, resp, &health)
	assertEqual(t, health["status"], "ok")

	// 2. Version
	resp = httpGet(t, base+"/api/v1/version")
	assertStatus(t, resp, 200)
	var ver map[string]string
	decodeJSON(t, resp, &ver)
	assertEqual(t, ver["version"], "test-e2e")
	assertEqual(t, ver["vmcore_version"], "mock-0.1.0")

	// 3. Create VM (auto backend → rustvmm)
	vm1 := createVM(t, base, map[string]any{
		"name": "e2e-web-01", "vcpus": 4, "memory_mb": 8192,
	})
	assertEqual(t, vm1["name"].(string), "e2e-web-01")
	assertEqual(t, vm1["backend"].(string), "rustvmm")
	assertEqual(t, vm1["state"].(string), "configured")
	id1 := fmt.Sprintf("%.0f", vm1["id"].(float64))

	// 4. Create second VM with explicit backend
	vm2 := createVM(t, base, map[string]any{
		"name": "e2e-micro-fn", "vcpus": 1, "memory_mb": 256, "backend": "rustvmm",
	})
	id2 := fmt.Sprintf("%.0f", vm2["id"].(float64))

	// 5. List VMs (should have 2)
	resp = httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
	var vms []map[string]any
	decodeJSON(t, resp, &vms)
	if len(vms) < 2 {
		t.Fatalf("expected at least 2 VMs, got %d", len(vms))
	}

	// 6. Get single VM
	resp = httpGet(t, base+"/api/v1/vms/"+id1)
	assertStatus(t, resp, 200)
	var vmDetail map[string]any
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["name"].(string), "e2e-web-01")

	// 7. Start VM
	resp = httpPost(t, base+"/api/v1/vms/"+id1+"/start", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "running")

	// 8. Pause VM
	resp = httpPost(t, base+"/api/v1/vms/"+id1+"/pause", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "paused")

	// 9. Resume VM
	resp = httpPost(t, base+"/api/v1/vms/"+id1+"/resume", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "running")

	// 10. Stop VM
	resp = httpPost(t, base+"/api/v1/vms/"+id1+"/stop", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "stopped")

	// 11. Delete VM
	resp = httpDelete(t, base+"/api/v1/vms/"+id1)
	assertStatus(t, resp, 204)

	// 12. Verify deleted (404)
	resp = httpGet(t, base+"/api/v1/vms/"+id1)
	assertStatus(t, resp, 404)

	// 13. Cleanup second VM
	resp = httpDelete(t, base+"/api/v1/vms/"+id2)
	assertStatus(t, resp, 204)

	// 14. List should be empty (for our created VMs)
	resp = httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
}

// ── E2E: Invalid State Transitions ───────────────────────

func TestE2E_InvalidStateTransitions(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	vm := createVM(t, base, map[string]any{"name": "state-test", "vcpus": 1, "memory_mb": 256})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))

	// configured → pause (invalid: must start first)
	resp := httpPost(t, base+"/api/v1/vms/"+id+"/pause", nil)
	assertStatus(t, resp, 409) // Conflict

	// configured → resume (invalid)
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/resume", nil)
	assertStatus(t, resp, 409)

	// Start then try start again (invalid: already running)
	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)
	assertStatus(t, resp, 409)

	httpDelete(t, base+"/api/v1/vms/"+id)
}

// ── E2E: Backend Selection ───────────────────────────────

func TestE2E_BackendSelection(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// List backends — should have both rustvmm and qemu
	resp := httpGet(t, base+"/api/v1/backends")
	assertStatus(t, resp, 200)
	var backends []map[string]any
	decodeJSON(t, resp, &backends)
	if len(backends) < 2 {
		t.Fatalf("expected at least 2 backends, got %d", len(backends))
	}
	backendNames := make(map[string]bool)
	for _, b := range backends {
		backendNames[b["name"].(string)] = true
	}
	if !backendNames["rustvmm"] {
		t.Fatal("rustvmm backend not found")
	}
	if !backendNames["qemu"] {
		t.Fatal("qemu backend not found")
	}

	// Invalid backend should fail
	body, _ := json.Marshal(map[string]any{
		"name": "bad-backend-vm", "backend": "nonexistent",
	})
	resp = httpPostRaw(t, base+"/api/v1/vms", body)
	assertStatus(t, resp, 500)
}

// ── E2E: QEMU Backend Lifecycle ─────────────────────────

func TestE2E_QEMUBackendLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create VM with explicit qemu backend
	vm := createVM(t, base, map[string]any{
		"name": "qemu-windows-01", "vcpus": 4, "memory_mb": 16384, "backend": "qemu",
	})
	assertEqual(t, vm["backend"].(string), "qemu")
	assertEqual(t, vm["state"].(string), "configured")
	id := fmt.Sprintf("%.0f", vm["id"].(float64))

	// Start
	resp := httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)
	assertStatus(t, resp, 200)
	var vmDetail map[string]any
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "running")

	// Pause
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/pause", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "paused")

	// Resume
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/resume", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "running")

	// Stop
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/stop", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["state"].(string), "stopped")

	// Delete
	resp = httpDelete(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 204)

	// Verify deleted
	resp = httpGet(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 404)
}

// ── E2E: Mixed Backend VMs ──────────────────────────────

func TestE2E_MixedBackends(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create rustvmm VM (microVM)
	vm1 := createVM(t, base, map[string]any{
		"name": "micro-fn-01", "vcpus": 1, "memory_mb": 256, "backend": "rustvmm",
	})
	assertEqual(t, vm1["backend"].(string), "rustvmm")

	// Create qemu VM (Windows)
	vm2 := createVM(t, base, map[string]any{
		"name": "win-server-01", "vcpus": 8, "memory_mb": 32768, "backend": "qemu",
	})
	assertEqual(t, vm2["backend"].(string), "qemu")

	// List should contain both
	resp := httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
	var vms []map[string]any
	decodeJSON(t, resp, &vms)
	if len(vms) < 2 {
		t.Fatalf("expected at least 2 VMs, got %d", len(vms))
	}

	// Both should be independently controllable
	id1 := fmt.Sprintf("%.0f", vm1["id"].(float64))
	id2 := fmt.Sprintf("%.0f", vm2["id"].(float64))

	resp = httpPost(t, base+"/api/v1/vms/"+id1+"/start", nil)
	assertStatus(t, resp, 200)
	resp = httpPost(t, base+"/api/v1/vms/"+id2+"/start", nil)
	assertStatus(t, resp, 200)

	// Cleanup
	httpDelete(t, base+"/api/v1/vms/"+id1)
	httpDelete(t, base+"/api/v1/vms/"+id2)
}

// ── E2E: Concurrent VM Operations ────────────────────────

func TestE2E_ConcurrentVMCreation(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	const count = 10
	results := make(chan error, count)

	for i := 0; i < count; i++ {
		go func(idx int) {
			body, _ := json.Marshal(map[string]any{
				"name":      fmt.Sprintf("concurrent-vm-%d", idx),
				"vcpus":     1,
				"memory_mb": 256,
			})
			resp, err := http.Post(base+"/api/v1/vms", "application/json", bytes.NewReader(body))
			if err != nil {
				results <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 201 {
				body, _ := io.ReadAll(resp.Body)
				results <- fmt.Errorf("vm-%d: status %d: %s", idx, resp.StatusCode, string(body))
				return
			}
			results <- nil
		}(i)
	}

	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent create failed: %v", err)
		}
	}

	// Verify all created
	resp := httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
	var vms []map[string]any
	decodeJSON(t, resp, &vms)
	if len(vms) < count {
		t.Errorf("expected at least %d VMs, got %d", count, len(vms))
	}
}

// ── E2E: Stub Endpoints ──────────────────────────────────

func TestE2E_StubEndpoints(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	endpoints := []string{
		"/api/v1/nodes",
		"/api/v1/storage/pools",
		"/api/v1/network/zones",
		"/api/v1/cluster/status",
	}
	for _, ep := range endpoints {
		resp := httpGet(t, base+ep)
		assertStatus(t, resp, 200)
	}
}

// ── E2E: Middleware ───────────────────────────────────────

func TestE2E_MiddlewareChain(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Request ID header present
	resp := httpGet(t, base+"/healthz")
	assertStatus(t, resp, 200)
	reqID := resp.Header.Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("expected X-Request-Id header")
	}

	// CORS preflight
	req, _ := http.NewRequest("OPTIONS", base+"/api/v1/vms", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 204 {
		t.Fatalf("expected 204 for OPTIONS, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header")
	}
}

// ═══ HTTP Helpers ════════════════════════════════════════

func httpGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func httpPost(t *testing.T, url string, payload any) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
	}
	resp, err := http.Post(url, "application/json", body)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func httpPostRaw(t *testing.T, url string, data []byte) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func httpDelete(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	return resp
}

func createVM(t *testing.T, base string, params map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(params)
	resp := httpPostRaw(t, base+"/api/v1/vms", body)
	assertStatus(t, resp, 201)
	var vm map[string]any
	decodeJSON(t, resp, &vm)
	return vm
}

func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status %d, got %d: %s", expected, resp.StatusCode, string(body))
	}
}

func assertEqual(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
}
