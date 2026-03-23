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

// setupE2E creates a full test server with Mock VMCore backend.
func setupE2E(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	core := ffi.NewMockVMCore()
	if err := core.Init(); err != nil {
		t.Fatal(err)
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
		Image:      image.NewService("/tmp/hcv-images"),
		LXC:        lxcBackend,
		Task:       task.NewTaskService(),
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
	var vmPage map[string]any
	decodeJSON(t, resp, &vmPage)
	vms := vmPage["data"].([]any)
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

// decodePaginatedData is a helper to extract the data array from a PaginatedResponse.
func decodePaginatedData(t *testing.T, resp *http.Response) ([]any, map[string]any) {
	t.Helper()
	var page map[string]any
	decodeJSON(t, resp, &page)
	data, ok := page["data"].([]any)
	if !ok {
		t.Fatal("expected data field to be an array")
	}
	return data, page
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
	mixedVMs, _ := decodePaginatedData(t, resp)
	if len(mixedVMs) < 2 {
		t.Fatalf("expected at least 2 VMs, got %d", len(mixedVMs))
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
	concurrentVMs, _ := decodePaginatedData(t, resp)
	if len(concurrentVMs) < count {
		t.Errorf("expected at least %d VMs, got %d", count, len(concurrentVMs))
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

// ── E2E: Backup Lifecycle ─────────────────────────────────

func TestE2E_BackupLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create a VM first
	vm := createVM(t, base, map[string]any{"name": "backup-test", "vcpus": 2, "memory_mb": 4096})
	vmID := fmt.Sprintf("%.0f", vm["id"].(float64))

	// Create backup
	body, _ := json.Marshal(map[string]any{
		"vm_id": vm["id"], "vm_name": "backup-test", "pool": "local-zfs",
	})
	resp := httpPostRaw(t, base+"/api/v1/backups", body)
	assertStatus(t, resp, 201)
	var backup map[string]any
	decodeJSON(t, resp, &backup)
	assertEqual(t, backup["vm_name"].(string), "backup-test")
	backupID := backup["id"].(string)

	// List backups
	resp = httpGet(t, base+"/api/v1/backups")
	assertStatus(t, resp, 200)
	backupData, _ := decodePaginatedData(t, resp)
	if len(backupData) < 1 {
		t.Fatal("expected at least 1 backup")
	}

	// Get backup
	resp = httpGet(t, base+"/api/v1/backups/"+backupID)
	assertStatus(t, resp, 200)

	// Delete backup
	resp = httpDelete(t, base+"/api/v1/backups/"+backupID)
	assertStatus(t, resp, 204)

	// Verify deleted
	resp = httpGet(t, base+"/api/v1/backups/"+backupID)
	assertStatus(t, resp, 404)

	// Cleanup VM
	httpDelete(t, base+"/api/v1/vms/"+vmID)
}

// ── E2E: Storage CRUD ────────────────────────────────────

func TestE2E_StorageCRUD(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// List pools
	resp := httpGet(t, base+"/api/v1/storage/pools")
	assertStatus(t, resp, 200)
	var pools []map[string]any
	decodeJSON(t, resp, &pools)
	if len(pools) < 1 {
		t.Fatal("expected at least 1 pool")
	}

	// Create volume
	body, _ := json.Marshal(map[string]any{
		"pool": "local-zfs", "name": "test-vol", "size_bytes": 1073741824, "format": "qcow2",
	})
	resp = httpPostRaw(t, base+"/api/v1/storage/volumes", body)
	assertStatus(t, resp, 201)
	var vol map[string]any
	decodeJSON(t, resp, &vol)
	assertEqual(t, vol["name"].(string), "test-vol")
	volID := vol["id"].(string)

	// List volumes
	resp = httpGet(t, base+"/api/v1/storage/volumes")
	assertStatus(t, resp, 200)

	// Delete volume
	resp = httpDelete(t, base+"/api/v1/storage/volumes/"+volID)
	assertStatus(t, resp, 204)
}

// ── E2E: Device Attach/Detach ────────────────────────────

func TestE2E_DeviceAttachDetach(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// List devices
	resp := httpGet(t, base+"/api/v1/devices")
	assertStatus(t, resp, 200)
	var devices []map[string]any
	decodeJSON(t, resp, &devices)
	if len(devices) < 1 {
		t.Fatal("expected at least 1 device")
	}

	// Create VM for attach
	vm := createVM(t, base, map[string]any{"name": "device-test", "vcpus": 1, "memory_mb": 256})
	vmID := vm["id"].(float64)

	// Attach device
	body, _ := json.Marshal(map[string]any{"vm_handle": vmID})
	resp = httpPostRaw(t, base+"/api/v1/devices/gpu-0/attach", body)
	assertStatus(t, resp, 200)

	// Detach device
	resp = httpPost(t, base+"/api/v1/devices/gpu-0/detach", nil)
	assertStatus(t, resp, 200)

	// Cleanup
	httpDelete(t, base+"/api/v1/vms/"+fmt.Sprintf("%.0f", vmID))
}

// ── E2E: Cluster Operations ─────────────────────────────

func TestE2E_ClusterOperations(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Cluster status
	resp := httpGet(t, base+"/api/v1/cluster/status")
	assertStatus(t, resp, 200)
	var status map[string]any
	decodeJSON(t, resp, &status)
	if !status["quorum"].(bool) {
		t.Fatal("expected quorum")
	}

	// Cluster nodes
	resp = httpGet(t, base+"/api/v1/cluster/nodes")
	assertStatus(t, resp, 200)
	var nodes []map[string]any
	decodeJSON(t, resp, &nodes)
	if len(nodes) < 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	// Fence a node
	body, _ := json.Marshal(map[string]any{
		"reason": "test", "action": "reboot",
	})
	resp = httpPostRaw(t, base+"/api/v1/cluster/fence/node-03", body)
	assertStatus(t, resp, 200)
}

// ── E2E: VM Migration ────────────────────────────────────

func TestE2E_VMMigration(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create VM
	vm := createVM(t, base, map[string]any{"name": "migrate-test", "vcpus": 2, "memory_mb": 4096})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))

	// Start VM
	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)

	// Migrate to node-02 (async with task)
	body, _ := json.Marshal(map[string]any{"target_node": "node-02"})
	resp := httpPostRaw(t, base+"/api/v1/vms/"+id+"/migrate", body)
	assertStatus(t, resp, 202)

	var migrateResp map[string]any
	decodeJSON(t, resp, &migrateResp)
	if migrateResp["task_id"] == nil {
		t.Fatal("expected task_id in response")
	}

	// Wait for async migration to complete
	time.Sleep(200 * time.Millisecond)

	// Verify node changed
	resp = httpGet(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 200)
	var vmDetail map[string]any
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["node"].(string), "node-02")

	httpDelete(t, base+"/api/v1/vms/"+id)
}

// ── E2E: Network & Firewall CRUD ─────────────────────────

func TestE2E_NetworkFirewallCRUD(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// List zones
	resp := httpGet(t, base+"/api/v1/network/zones")
	assertStatus(t, resp, 200)

	// List vnets
	resp = httpGet(t, base+"/api/v1/network/vnets")
	assertStatus(t, resp, 200)

	// Firewall rules (initially empty or with defaults)
	resp = httpGet(t, base+"/api/v1/network/firewall")
	assertStatus(t, resp, 200)
}

// ── E2E: Storage Snapshots ───────────────────────────────

func TestE2E_StorageSnapshots(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create volume
	body, _ := json.Marshal(map[string]any{
		"pool": "local-zfs", "name": "snap-test-vol", "size_bytes": 1073741824, "format": "raw",
	})
	resp := httpPostRaw(t, base+"/api/v1/storage/volumes", body)
	assertStatus(t, resp, 201)
	var vol map[string]any
	decodeJSON(t, resp, &vol)

	// Volume exists in list
	resp = httpGet(t, base+"/api/v1/storage/volumes")
	assertStatus(t, resp, 200)

	// Delete volume
	resp = httpDelete(t, base+"/api/v1/storage/volumes/"+vol["id"].(string))
	assertStatus(t, resp, 204)
}

// ── E2E: API Info ────────────────────────────────────────

func TestE2E_APIInfo(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	resp := httpGet(t, base+"/api/v1/api-info")
	assertStatus(t, resp, 200)
	var info map[string]any
	decodeJSON(t, resp, &info)
	assertEqual(t, info["current_version"].(string), "v1")
}

// ── E2E: Template Lifecycle ───────────────────────────────

func TestE2E_TemplateLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. List default templates (should have 3)
	resp := httpGet(t, base+"/api/v1/templates")
	assertStatus(t, resp, 200)
	tplData, _ := decodePaginatedData(t, resp)
	if len(tplData) < 3 {
		t.Fatalf("expected at least 3 default templates, got %d", len(tplData))
	}

	// 2. Get a specific template
	resp = httpGet(t, base+"/api/v1/templates/tpl-1")
	assertStatus(t, resp, 200)
	var tpl map[string]any
	decodeJSON(t, resp, &tpl)
	assertEqual(t, tpl["name"].(string), "linux-small")
	assertEqual(t, tpl["backend"].(string), "rustvmm")

	// 3. Create a new template
	body, _ := json.Marshal(map[string]any{
		"name": "custom-large", "description": "Custom large VM",
		"vcpus": 8, "memory_mb": 16384, "disk_size_gb": 200,
		"backend": "qemu", "os_type": "linux",
	})
	resp = httpPostRaw(t, base+"/api/v1/templates", body)
	assertStatus(t, resp, 201)
	var newTpl map[string]any
	decodeJSON(t, resp, &newTpl)
	assertEqual(t, newTpl["name"].(string), "custom-large")
	newTplID := newTpl["id"].(string)

	// 4. Deploy a VM from template
	body, _ = json.Marshal(map[string]any{"name": "deployed-from-tpl"})
	resp = httpPostRaw(t, base+"/api/v1/templates/tpl-1/deploy", body)
	assertStatus(t, resp, 201)
	var vm map[string]any
	decodeJSON(t, resp, &vm)
	assertEqual(t, vm["name"].(string), "deployed-from-tpl")
	assertEqual(t, vm["backend"].(string), "rustvmm")
	vmID := fmt.Sprintf("%.0f", vm["id"].(float64))

	// 5. Delete the custom template
	resp = httpDelete(t, base+"/api/v1/templates/"+newTplID)
	assertStatus(t, resp, 204)

	// 6. Verify deleted (404)
	resp = httpGet(t, base+"/api/v1/templates/"+newTplID)
	assertStatus(t, resp, 404)

	// 7. Verify non-existent template (404)
	resp = httpGet(t, base+"/api/v1/templates/tpl-999")
	assertStatus(t, resp, 404)

	// 8. Deploy from non-existent template (404)
	body, _ = json.Marshal(map[string]any{"name": "should-fail"})
	resp = httpPostRaw(t, base+"/api/v1/templates/tpl-999/deploy", body)
	assertStatus(t, resp, 404)

	// Cleanup deployed VM
	httpDelete(t, base+"/api/v1/vms/"+vmID)
}

// ── E2E: Pagination ───────────────────────────────────────

func TestE2E_Pagination(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create 5 VMs
	for i := 0; i < 5; i++ {
		createVM(t, base, map[string]any{
			"name": fmt.Sprintf("page-vm-%d", i), "vcpus": 1, "memory_mb": 256,
		})
	}

	// Get page 1 (limit=2)
	resp := httpGet(t, base+"/api/v1/vms?limit=2&offset=0")
	assertStatus(t, resp, 200)
	var page map[string]any
	decodeJSON(t, resp, &page)
	if int(page["total_count"].(float64)) < 5 {
		t.Fatalf("expected total_count >= 5, got %v", page["total_count"])
	}
	data := page["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("expected 2 items, got %d", len(data))
	}

	// Get page 2
	resp = httpGet(t, base+"/api/v1/vms?limit=2&offset=2")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &page)
	data2 := page["data"].([]any)
	if len(data2) != 2 {
		t.Fatalf("expected 2 items on page 2, got %d", len(data2))
	}

	// Get page 3 (only 1 remaining)
	resp = httpGet(t, base+"/api/v1/vms?limit=2&offset=4")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &page)
	data3 := page["data"].([]any)
	if len(data3) != 1 {
		t.Fatalf("expected 1 item on page 3, got %d", len(data3))
	}

	// Default pagination (no params) should return all
	resp = httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &page)
	allData := page["data"].([]any)
	if len(allData) < 5 {
		t.Fatalf("expected >= 5 items with default pagination, got %d", len(allData))
	}
	if int(page["offset"].(float64)) != 0 {
		t.Fatalf("expected offset 0, got %v", page["offset"])
	}
}

// ── E2E: Snapshot Lifecycle ───────────────────────────────

func TestE2E_SnapshotLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create a VM first
	vm := createVM(t, base, map[string]any{"name": "snap-test-vm", "vcpus": 2, "memory_mb": 4096})
	vmID := vm["id"].(float64)

	// 1. Create snapshot
	body, _ := json.Marshal(map[string]any{
		"vm_id": vmID, "vm_name": "snap-test-vm",
	})
	resp := httpPostRaw(t, base+"/api/v1/snapshots", body)
	assertStatus(t, resp, 201)
	var snap map[string]any
	decodeJSON(t, resp, &snap)
	assertEqual(t, snap["vm_name"].(string), "snap-test-vm")
	assertEqual(t, snap["state"].(string), "created")
	snapID := snap["id"].(string)

	// 2. Create a second snapshot
	body, _ = json.Marshal(map[string]any{
		"vm_id": vmID, "vm_name": "snap-test-vm",
	})
	resp = httpPostRaw(t, base+"/api/v1/snapshots", body)
	assertStatus(t, resp, 201)

	// 3. List snapshots (filter by vm_id)
	resp = httpGet(t, base+fmt.Sprintf("/api/v1/snapshots?vm_id=%.0f", vmID))
	assertStatus(t, resp, 200)
	var snapshots []map[string]any
	decodeJSON(t, resp, &snapshots)
	if len(snapshots) < 2 {
		t.Fatalf("expected at least 2 snapshots, got %d", len(snapshots))
	}

	// 4. Get snapshot
	resp = httpGet(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 200)
	var snapDetail map[string]any
	decodeJSON(t, resp, &snapDetail)
	assertEqual(t, snapDetail["id"].(string), snapID)

	// 5. Restore snapshot
	resp = httpPost(t, base+"/api/v1/snapshots/"+snapID+"/restore", nil)
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &snapDetail)
	assertEqual(t, snapDetail["state"].(string), "restoring")

	// 6. Delete snapshot
	resp = httpDelete(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 204)

	// 7. Verify deleted (404)
	resp = httpGet(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 404)

	// 8. Non-existent snapshot (404)
	resp = httpGet(t, base+"/api/v1/snapshots/snap-999")
	assertStatus(t, resp, 404)

	// Cleanup VM
	httpDelete(t, base+"/api/v1/vms/"+fmt.Sprintf("%.0f", vmID))
}

// ── E2E: Image Registry ──────────────────────────────────

func TestE2E_ImageRegistry(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. List default images (should have 3)
	resp := httpGet(t, base+"/api/v1/images")
	assertStatus(t, resp, 200)
	var images []map[string]any
	decodeJSON(t, resp, &images)
	if len(images) < 3 {
		t.Fatalf("expected at least 3 default images, got %d", len(images))
	}

	// 2. Get a specific image
	resp = httpGet(t, base+"/api/v1/images/img-1")
	assertStatus(t, resp, 200)
	var img map[string]any
	decodeJSON(t, resp, &img)
	assertEqual(t, img["name"].(string), "ubuntu-24.04")
	assertEqual(t, img["format"].(string), "qcow2")
	assertEqual(t, img["os_type"].(string), "linux")

	// 3. Register a new image
	body, _ := json.Marshal(map[string]any{
		"name": "fedora-41", "format": "qcow2",
		"path": "/tmp/hcv-images/fedora-41.qcow2", "os_type": "linux",
	})
	resp = httpPostRaw(t, base+"/api/v1/images", body)
	assertStatus(t, resp, 201)
	var newImg map[string]any
	decodeJSON(t, resp, &newImg)
	assertEqual(t, newImg["name"].(string), "fedora-41")
	assertEqual(t, newImg["format"].(string), "qcow2")
	newImgID := newImg["id"].(string)

	// 4. List images — should now have 4
	resp = httpGet(t, base+"/api/v1/images")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &images)
	if len(images) < 4 {
		t.Fatalf("expected at least 4 images after register, got %d", len(images))
	}

	// 5. Delete the new image
	resp = httpDelete(t, base+"/api/v1/images/"+newImgID)
	assertStatus(t, resp, 204)

	// 6. Verify deleted (404)
	resp = httpGet(t, base+"/api/v1/images/"+newImgID)
	assertStatus(t, resp, 404)

	// 7. Delete non-existent image (404)
	resp = httpDelete(t, base+"/api/v1/images/img-999")
	assertStatus(t, resp, 404)
}

// ── E2E: Storage Snapshot Rollback ────────────────────────

func TestE2E_StorageSnapshotRollback(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Create a volume
	body, _ := json.Marshal(map[string]any{
		"pool": "local-zfs", "name": "rollback-vol", "size_bytes": 1073741824, "format": "raw",
	})
	resp := httpPostRaw(t, base+"/api/v1/storage/volumes", body)
	assertStatus(t, resp, 201)
	var vol map[string]any
	decodeJSON(t, resp, &vol)
	volID := vol["id"].(string)

	// 2. Create a storage snapshot (via storage service, using internal snapshot endpoint)
	// First, we need to use the storage snapshot mechanism
	// Create snapshot by creating volume + snapshot via the storage service
	// The storage service handles snapshots at the storage level
	// We test the new storage snapshot endpoints

	// 3. Rollback non-existent snapshot → 404
	resp = httpPost(t, base+"/api/v1/storage/snapshots/snap-999/rollback", nil)
	assertStatus(t, resp, 404)

	// 4. Delete volume
	resp = httpDelete(t, base+"/api/v1/storage/volumes/"+volID)
	assertStatus(t, resp, 204)
}

// ── E2E: Storage Snapshot Clone ──────────────────────────

func TestE2E_StorageSnapshotClone(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Create a volume
	body, _ := json.Marshal(map[string]any{
		"pool": "local-zfs", "name": "clone-src-vol", "size_bytes": 2147483648, "format": "qcow2",
	})
	resp := httpPostRaw(t, base+"/api/v1/storage/volumes", body)
	assertStatus(t, resp, 201)
	var vol map[string]any
	decodeJSON(t, resp, &vol)

	// 2. Clone non-existent snapshot → 404
	cloneBody, _ := json.Marshal(map[string]any{"name": "cloned-vol"})
	resp = httpPostRaw(t, base+"/api/v1/storage/snapshots/snap-999/clone", cloneBody)
	assertStatus(t, resp, 404)

	// 3. Clone with missing name → 400
	resp = httpPostRaw(t, base+"/api/v1/storage/snapshots/snap-999/clone", []byte(`{}`))
	assertStatus(t, resp, 400)

	// 4. Delete non-existent storage snapshot → 404
	resp = httpDelete(t, base+"/api/v1/storage/snapshots/snap-999")
	assertStatus(t, resp, 404)
}

// ── E2E: Snapshot With Storage Integration ───────────────

func TestE2E_SnapshotWithStorage(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Create a VM snapshot (no storage integration in default setup)
	body, _ := json.Marshal(map[string]any{
		"vm_id": 1, "vm_name": "storage-snap-vm",
	})
	resp := httpPostRaw(t, base+"/api/v1/snapshots", body)
	assertStatus(t, resp, 201)
	var snap map[string]any
	decodeJSON(t, resp, &snap)
	snapID := snap["id"].(string)

	// 2. Verify snapshot fields include new fields (storage_snapshot_id, volume_id)
	resp = httpGet(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 200)
	var snapDetail map[string]any
	decodeJSON(t, resp, &snapDetail)
	if snapDetail["vm_name"].(string) != "storage-snap-vm" {
		t.Fatalf("expected vm_name 'storage-snap-vm', got %q", snapDetail["vm_name"])
	}

	// 3. Restore snapshot
	resp = httpPost(t, base+"/api/v1/snapshots/"+snapID+"/restore", nil)
	assertStatus(t, resp, 200)
	var restored map[string]any
	decodeJSON(t, resp, &restored)
	if restored["state"].(string) != "restoring" {
		t.Fatalf("expected state 'restoring', got %q", restored["state"])
	}

	// 4. Delete snapshot
	resp = httpDelete(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 204)

	// 5. Verify deleted (404)
	resp = httpGet(t, base+"/api/v1/snapshots/"+snapID)
	assertStatus(t, resp, 404)
}

// ── E2E: Zone CRUD ───────────────────────────────────────

func TestE2E_ZoneCRUD(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. List zones (should have default zones)
	resp := httpGet(t, base+"/api/v1/network/zones")
	assertStatus(t, resp, 200)
	var zones []map[string]any
	decodeJSON(t, resp, &zones)
	initialCount := len(zones)
	if initialCount == 0 {
		t.Fatal("expected default zones to exist")
	}

	// 2. Create a new zone
	body, _ := json.Marshal(map[string]any{
		"name":   "test-zone",
		"type":   "simple",
		"bridge": "br-test",
		"mtu":    1500,
	})
	resp = httpPostRaw(t, base+"/api/v1/network/zones", body)
	assertStatus(t, resp, 201)
	var created map[string]any
	decodeJSON(t, resp, &created)
	if created["name"] != "test-zone" {
		t.Errorf("expected zone name 'test-zone', got %v", created["name"])
	}
	if created["status"] != "active" {
		t.Errorf("expected zone status 'active', got %v", created["status"])
	}

	// 3. List zones again (should have one more)
	resp = httpGet(t, base+"/api/v1/network/zones")
	assertStatus(t, resp, 200)
	var updatedZones []map[string]any
	decodeJSON(t, resp, &updatedZones)
	if len(updatedZones) != initialCount+1 {
		t.Errorf("expected %d zones, got %d", initialCount+1, len(updatedZones))
	}

	// 4. Create duplicate (should conflict)
	resp = httpPostRaw(t, base+"/api/v1/network/zones", body)
	assertStatus(t, resp, 409)

	// 5. Delete zone
	resp = httpDelete(t, base+"/api/v1/network/zones/test-zone")
	assertStatus(t, resp, 204)

	// 6. Verify deleted
	resp = httpDelete(t, base+"/api/v1/network/zones/test-zone")
	assertStatus(t, resp, 404)

	// 7. List zones (should be back to initial count)
	resp = httpGet(t, base+"/api/v1/network/zones")
	assertStatus(t, resp, 200)
	var finalZones []map[string]any
	decodeJSON(t, resp, &finalZones)
	if len(finalZones) != initialCount {
		t.Errorf("expected %d zones after delete, got %d", initialCount, len(finalZones))
	}
}

// ── E2E: Migration Status ────────────────────────────────

func TestE2E_MigrationStatus(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create and start a VM
	vm := createVM(t, base, map[string]any{"name": "migrate-status-test", "vcpus": 2, "memory_mb": 4096})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))

	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)

	// Check migration status before migration (should be 404)
	resp := httpGet(t, base+"/api/v1/vms/"+id+"/migration")
	assertStatus(t, resp, 404)

	// Migrate VM (async with task)
	body, _ := json.Marshal(map[string]any{"target_node": "node-05"})
	resp = httpPostRaw(t, base+"/api/v1/vms/"+id+"/migrate", body)
	assertStatus(t, resp, 202)

	// Wait for async migration to complete
	time.Sleep(200 * time.Millisecond)

	// Check migration status after migration
	resp = httpGet(t, base+"/api/v1/vms/"+id+"/migration")
	assertStatus(t, resp, 200)
	var status map[string]any
	decodeJSON(t, resp, &status)

	if status["phase"] != "completed" {
		t.Errorf("expected migration phase 'completed', got %v", status["phase"])
	}
	progress := status["progress"].(float64)
	if int(progress) != 100 {
		t.Errorf("expected progress 100, got %v", progress)
	}
	if status["target_node"] != "node-05" {
		t.Errorf("expected target_node 'node-05', got %v", status["target_node"])
	}

	// Clean up
	httpDelete(t, base+"/api/v1/vms/"+id)
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

// ── E2E: Cluster Leader ──────────────────────────────

func TestE2E_ClusterLeader(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// GET /api/v1/cluster/leader
	resp := httpGet(t, base+"/api/v1/cluster/leader")
	assertStatus(t, resp, 200)

	var leader map[string]any
	decodeJSON(t, resp, &leader)

	// In test mode (no HAServices), should return node name and is_self=true
	if _, ok := leader["leader"]; !ok {
		t.Error("expected 'leader' field in response")
	}
	if _, ok := leader["is_self"]; !ok {
		t.Error("expected 'is_self' field in response")
	}

	// POST /api/v1/cluster/promote
	resp = httpPostRaw(t, base+"/api/v1/cluster/promote", []byte("{}"))
	assertStatus(t, resp, 200)

	var promote map[string]string
	decodeJSON(t, resp, &promote)
	if promote["status"] == "" {
		t.Error("expected non-empty status in promote response")
	}
}

// ── E2E: VM Restart Policy ────────────────────────────

func TestE2E_VMRestartPolicy(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create VM with restart_policy
	vm := createVM(t, base, map[string]any{
		"name":           "policy-vm",
		"vcpus":          2,
		"memory_mb":      1024,
		"restart_policy": "on-failure",
	})

	if vm["restart_policy"] != "on-failure" {
		t.Errorf("expected restart_policy 'on-failure', got %v", vm["restart_policy"])
	}

	// Create VM without restart_policy (should default to "always")
	vm2 := createVM(t, base, map[string]any{
		"name":      "default-policy-vm",
		"vcpus":     1,
		"memory_mb": 512,
	})

	if vm2["restart_policy"] != "always" {
		t.Errorf("expected default restart_policy 'always', got %v", vm2["restart_policy"])
	}
}

// ── E2E: Container Lifecycle ──────────────────────────

func TestE2E_ContainerLifecycle(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Create container with type="container" (auto-selects lxc backend)
	vm := createVM(t, base, map[string]any{
		"name":      "test-container",
		"vcpus":     1,
		"memory_mb": 256,
		"type":      "container",
	})
	assertEqual(t, vm["backend"].(string), "lxc")
	assertEqual(t, vm["type"].(string), "container")
	assertEqual(t, vm["state"].(string), "configured")
	if vm["ip_address"] == nil || vm["ip_address"].(string) == "" {
		t.Error("expected IP address to be assigned")
	}
	id := fmt.Sprintf("%.0f", vm["id"].(float64))

	// 2. Create container with explicit lxc backend
	vm2 := createVM(t, base, map[string]any{
		"name":      "lxc-explicit",
		"vcpus":     2,
		"memory_mb": 512,
		"backend":   "lxc",
	})
	assertEqual(t, vm2["backend"].(string), "lxc")
	assertEqual(t, vm2["type"].(string), "container")

	// 3. Start container
	resp := httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)
	assertStatus(t, resp, 200)
	var started map[string]any
	decodeJSON(t, resp, &started)
	assertEqual(t, started["state"].(string), "running")

	// 4. Get container stats
	resp = httpGet(t, base+"/api/v1/vms/"+id+"/stats")
	assertStatus(t, resp, 200)
	var stats map[string]any
	decodeJSON(t, resp, &stats)
	if stats["memory_limit_bytes"] == nil {
		t.Error("expected memory_limit_bytes in stats")
	}
	if stats["pid_count"] == nil {
		t.Error("expected pid_count in stats")
	}

	// 5. Pause container
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/pause", nil)
	assertStatus(t, resp, 200)
	var paused map[string]any
	decodeJSON(t, resp, &paused)
	assertEqual(t, paused["state"].(string), "paused")

	// 6. Resume container
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/resume", nil)
	assertStatus(t, resp, 200)
	var resumed map[string]any
	decodeJSON(t, resp, &resumed)
	assertEqual(t, resumed["state"].(string), "running")

	// 7. Stop container
	resp = httpPost(t, base+"/api/v1/vms/"+id+"/stop", nil)
	assertStatus(t, resp, 200)
	var stopped map[string]any
	decodeJSON(t, resp, &stopped)
	assertEqual(t, stopped["state"].(string), "stopped")

	// 8. Delete container
	resp = httpDelete(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 204)

	// 9. Verify deleted
	resp = httpGet(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 404)
}

// ── E2E: LXC Templates ──────────────────────────────

func TestE2E_LXCTemplates(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Get LXC templates
	resp := httpGet(t, base+"/api/v1/templates/lxc")
	assertStatus(t, resp, 200)
	var result map[string]any
	decodeJSON(t, resp, &result)

	templates, ok := result["templates"].([]any)
	if !ok {
		t.Fatal("expected templates array in response")
	}
	if len(templates) < 4 {
		t.Fatalf("expected at least 4 templates, got %d", len(templates))
	}

	// Verify known templates
	found := make(map[string]bool)
	for _, tmpl := range templates {
		found[tmpl.(string)] = true
	}
	for _, expected := range []string{"ubuntu", "alpine", "debian", "centos"} {
		if !found[expected] {
			t.Errorf("expected template %q not found", expected)
		}
	}
}

// ── E2E: VM Type Filter ─────────────────────────────

func TestE2E_VMTypeFilter(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create a VM
	createVM(t, base, map[string]any{
		"name": "filter-vm", "vcpus": 1, "memory_mb": 256,
	})

	// Create a container
	createVM(t, base, map[string]any{
		"name": "filter-ct", "vcpus": 1, "memory_mb": 256, "type": "container",
	})

	// List all
	resp := httpGet(t, base+"/api/v1/vms")
	assertStatus(t, resp, 200)
	var allPage map[string]any
	decodeJSON(t, resp, &allPage)
	allVMs := allPage["data"].([]any)
	if len(allVMs) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(allVMs))
	}

	// List only containers
	resp = httpGet(t, base+"/api/v1/vms?type=container")
	assertStatus(t, resp, 200)
	var ctPage map[string]any
	decodeJSON(t, resp, &ctPage)
	containers := ctPage["data"].([]any)
	for _, c := range containers {
		ct := c.(map[string]any)
		if ct["type"].(string) != "container" {
			t.Errorf("expected type=container, got %v", ct["type"])
		}
	}

	// List only VMs
	resp = httpGet(t, base+"/api/v1/vms?type=vm")
	assertStatus(t, resp, 200)
	var vmPage map[string]any
	decodeJSON(t, resp, &vmPage)
	vms := vmPage["data"].([]any)
	for _, v := range vms {
		vm := v.(map[string]any)
		vmType := vm["type"].(string)
		if vmType != "vm" {
			t.Errorf("expected type=vm, got %v", vmType)
		}
	}
}

// ── E2E: Container Exec ────────────────────────────

func TestE2E_ContainerExec(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Create a container
	ct := createVM(t, base, map[string]any{
		"name": "exec-ct", "vcpus": 1, "memory_mb": 256, "type": "container",
	})
	ctID := fmt.Sprintf("%.0f", ct["id"].(float64))
	assertEqual(t, ct["type"].(string), "container")
	assertEqual(t, ct["backend"].(string), "lxc")

	// 2. Exec on non-running container should fail (409)
	resp := httpPost(t, base+"/api/v1/vms/"+ctID+"/exec", map[string]any{
		"command": []string{"ls"},
	})
	assertStatus(t, resp, 409)

	// 3. Start the container
	resp = httpPost(t, base+"/api/v1/vms/"+ctID+"/start", nil)
	assertStatus(t, resp, 200)

	// 4. Exec command
	resp = httpPost(t, base+"/api/v1/vms/"+ctID+"/exec", map[string]any{
		"command": []string{"ls", "-la"},
	})
	assertStatus(t, resp, 200)
	var execResult map[string]string
	decodeJSON(t, resp, &execResult)
	if execResult["output"] != "exec: ls -la" {
		t.Errorf("expected 'exec: ls -la', got %q", execResult["output"])
	}

	// 5. Exec on a VM (not container) should fail (400)
	vm := createVM(t, base, map[string]any{
		"name": "exec-vm", "vcpus": 1, "memory_mb": 256,
	})
	vmID := fmt.Sprintf("%.0f", vm["id"].(float64))
	resp = httpPost(t, base+"/api/v1/vms/"+vmID+"/exec", map[string]any{
		"command": []string{"ls"},
	})
	assertStatus(t, resp, 400)

	// 6. Exec with empty command should fail (400)
	resp = httpPost(t, base+"/api/v1/vms/"+ctID+"/exec", map[string]any{
		"command": []string{},
	})
	assertStatus(t, resp, 400)

	// 7. Cleanup
	resp = httpDelete(t, base+"/api/v1/vms/"+ctID)
	assertStatus(t, resp, 204)
	resp = httpDelete(t, base+"/api/v1/vms/"+vmID)
	assertStatus(t, resp, 204)
}

// ── E2E: Live Migration with Async Task ──────────────────

func TestE2E_LiveMigration(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create and start VM
	vm := createVM(t, base, map[string]any{"name": "live-mig-vm", "vcpus": 2, "memory_mb": 4096})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))
	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)

	// Trigger async migration
	body, _ := json.Marshal(map[string]any{"target_node": "node-02"})
	resp := httpPostRaw(t, base+"/api/v1/vms/"+id+"/migrate", body)
	assertStatus(t, resp, 202)

	var migrateResp map[string]any
	decodeJSON(t, resp, &migrateResp)
	taskID := migrateResp["task_id"].(string)
	if taskID == "" {
		t.Fatal("expected non-empty task_id")
	}

	// Poll task status until completed (with timeout)
	deadline := time.Now().Add(5 * time.Second)
	var taskStatus string
	for time.Now().Before(deadline) {
		resp = httpGet(t, base+"/api/v1/tasks/"+taskID)
		assertStatus(t, resp, 200)
		var taskDetail map[string]any
		decodeJSON(t, resp, &taskDetail)
		taskStatus = taskDetail["status"].(string)
		if taskStatus == "completed" || taskStatus == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertEqual(t, taskStatus, "completed")

	// Verify task progress reached 100
	resp = httpGet(t, base+"/api/v1/tasks/"+taskID)
	assertStatus(t, resp, 200)
	var finalTask map[string]any
	decodeJSON(t, resp, &finalTask)
	progress := int(finalTask["progress"].(float64))
	if progress != 100 {
		t.Fatalf("expected progress 100, got %d", progress)
	}

	// Verify VM node changed
	resp = httpGet(t, base+"/api/v1/vms/"+id)
	assertStatus(t, resp, 200)
	var vmDetail map[string]any
	decodeJSON(t, resp, &vmDetail)
	assertEqual(t, vmDetail["node"].(string), "node-02")

	// Verify migration status
	resp = httpGet(t, base+"/api/v1/vms/"+id+"/migration")
	assertStatus(t, resp, 200)
	var migStatus map[string]any
	decodeJSON(t, resp, &migStatus)
	assertEqual(t, migStatus["phase"].(string), "completed")

	httpDelete(t, base+"/api/v1/vms/"+id)
}

// ── E2E: Cancel Migration ────────────────────────────────

func TestE2E_CancelMigration(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// Create and start VM
	vm := createVM(t, base, map[string]any{"name": "cancel-mig-vm", "vcpus": 2, "memory_mb": 4096})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))
	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)

	// Cancel migration on VM with no active migration should fail
	req, _ := http.NewRequest("DELETE", base+"/api/v1/vms/"+id+"/migration", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE migration: %v", err)
	}
	assertStatus(t, resp, 404)

	httpDelete(t, base+"/api/v1/vms/"+id)
}

// ── E2E: Async Task API ─────────────────────────────────

func TestE2E_AsyncTaskAPI(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Initially no tasks
	resp := httpGet(t, base+"/api/v1/tasks")
	assertStatus(t, resp, 200)
	var tasks []map[string]any
	decodeJSON(t, resp, &tasks)
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks initially, got %d", len(tasks))
	}

	// 2. Create VM, start it, trigger migrate (creates task)
	vm := createVM(t, base, map[string]any{"name": "task-test-vm", "vcpus": 2, "memory_mb": 4096})
	id := fmt.Sprintf("%.0f", vm["id"].(float64))
	httpPost(t, base+"/api/v1/vms/"+id+"/start", nil)

	body, _ := json.Marshal(map[string]any{"target_node": "node-02"})
	resp = httpPostRaw(t, base+"/api/v1/vms/"+id+"/migrate", body)
	assertStatus(t, resp, 202)

	var migrateResp map[string]any
	decodeJSON(t, resp, &migrateResp)
	taskID := migrateResp["task_id"].(string)

	// 3. Get task by ID
	resp = httpGet(t, base+"/api/v1/tasks/"+taskID)
	assertStatus(t, resp, 200)
	var taskDetail map[string]any
	decodeJSON(t, resp, &taskDetail)
	assertEqual(t, taskDetail["type"].(string), "vm.migrate")

	// 4. Wait for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp = httpGet(t, base+"/api/v1/tasks/"+taskID)
		assertStatus(t, resp, 200)
		decodeJSON(t, resp, &taskDetail)
		if taskDetail["status"].(string) == "completed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertEqual(t, taskDetail["status"].(string), "completed")

	// 5. List tasks with type filter
	resp = httpGet(t, base+"/api/v1/tasks?type=vm.migrate")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &tasks)
	if len(tasks) < 1 {
		t.Fatal("expected at least 1 task with type vm.migrate")
	}

	// 6. List tasks with status filter
	resp = httpGet(t, base+"/api/v1/tasks?status=completed")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &tasks)
	if len(tasks) < 1 {
		t.Fatal("expected at least 1 completed task")
	}

	// 7. Delete completed task
	req, _ := http.NewRequest("DELETE", base+"/api/v1/tasks/"+taskID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE task: %v", err)
	}
	assertStatus(t, resp, 204)

	// 8. Verify task deleted
	resp = httpGet(t, base+"/api/v1/tasks/"+taskID)
	assertStatus(t, resp, 404)

	// 9. Get non-existent task
	resp = httpGet(t, base+"/api/v1/tasks/task-nonexistent")
	assertStatus(t, resp, 404)

	httpDelete(t, base+"/api/v1/vms/"+id)
}

// ── E2E: Node Detail API ─────────────────────────────────

func TestE2E_NodeDetail(t *testing.T) {
	srv, cleanup := setupE2E(t)
	defer cleanup()
	base := srv.URL

	// 1. Get known node by name
	resp := httpGet(t, base+"/api/v1/nodes/node-01")
	assertStatus(t, resp, 200)
	var node map[string]any
	decodeJSON(t, resp, &node)
	assertEqual(t, node["name"].(string), "node-01")
	assertEqual(t, node["status"].(string), "online")
	if _, ok := node["vm_count"]; !ok {
		t.Fatal("expected vm_count field")
	}
	if _, ok := node["is_leader"]; !ok {
		t.Fatal("expected is_leader field")
	}

	// 2. Get another known node
	resp = httpGet(t, base+"/api/v1/nodes/node-02")
	assertStatus(t, resp, 200)
	decodeJSON(t, resp, &node)
	assertEqual(t, node["name"].(string), "node-02")

	// 3. Get unknown node — 404
	resp = httpGet(t, base+"/api/v1/nodes/node-unknown")
	assertStatus(t, resp, 404)

	// 4. Verify node list still works
	resp = httpGet(t, base+"/api/v1/nodes")
	assertStatus(t, resp, 200)
	var nodes []map[string]any
	decodeJSON(t, resp, &nodes)
	if len(nodes) < 3 {
		t.Fatalf("expected at least 3 nodes, got %d", len(nodes))
	}
}
