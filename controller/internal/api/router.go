// Package api — REST API Gateway with middleware chain
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

// VersionInfo holds version metadata returned by /api/v1/version
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	VMCore    string `json:"vmcore_version"`
}

// Services aggregates all backend services for the API layer.
type Services struct {
	Compute    *compute.ComputeService
	Storage    *storage.Service
	Network    *network.Service
	Peripheral *peripheral.Service
	HA         *ha.Service
	Version    VersionInfo
}

// NewRouter creates the HTTP router with middleware.
// If svc is nil, stub handlers are used (for backwards compatibility).
func NewRouter(svc *Services) http.Handler {
	mux := http.NewServeMux()

	if svc != nil {
		// Live handlers backed by compute service
		mux.HandleFunc("GET /healthz", handleHealth)
		mux.HandleFunc("GET /api/v1/version", svc.handleVersion)
		mux.HandleFunc("GET /api/v1/vms", svc.handleListVMs)
		mux.HandleFunc("POST /api/v1/vms", svc.handleCreateVM)
		mux.HandleFunc("GET /api/v1/vms/{id}", svc.handleGetVM)
		mux.HandleFunc("DELETE /api/v1/vms/{id}", svc.handleDeleteVM)
		mux.HandleFunc("POST /api/v1/vms/{id}/start", svc.handleVMAction("start"))
		mux.HandleFunc("POST /api/v1/vms/{id}/stop", svc.handleVMAction("stop"))
		mux.HandleFunc("POST /api/v1/vms/{id}/pause", svc.handleVMAction("pause"))
		mux.HandleFunc("POST /api/v1/vms/{id}/resume", svc.handleVMAction("resume"))
		mux.HandleFunc("GET /api/v1/nodes", handleListNodes)
		mux.HandleFunc("GET /api/v1/backends", svc.handleListBackends)

		// Storage
		mux.HandleFunc("GET /api/v1/storage/pools", svc.handleStoragePools)
		mux.HandleFunc("GET /api/v1/storage/volumes", svc.handleStorageVolumes)
		mux.HandleFunc("POST /api/v1/storage/volumes", svc.handleCreateVolume)
		mux.HandleFunc("DELETE /api/v1/storage/volumes/{id}", svc.handleDeleteVolume)

		// Network
		mux.HandleFunc("GET /api/v1/network/zones", svc.handleNetworkZones)
		mux.HandleFunc("GET /api/v1/network/vnets", svc.handleNetworkVNets)
		mux.HandleFunc("GET /api/v1/network/firewall", svc.handleFirewallRules)

		// Peripheral
		mux.HandleFunc("GET /api/v1/devices", svc.handleListDevices)
		mux.HandleFunc("POST /api/v1/devices/{id}/attach", svc.handleAttachDevice)
		mux.HandleFunc("POST /api/v1/devices/{id}/detach", svc.handleDetachDevice)

		// HA / Cluster
		mux.HandleFunc("GET /api/v1/cluster/status", svc.handleClusterStatus)
		mux.HandleFunc("GET /api/v1/cluster/nodes", svc.handleClusterNodes)
		mux.HandleFunc("POST /api/v1/cluster/fence/{node}", svc.handleFenceNode)
	} else {
		// Legacy stub handlers (no compute service)
		mux.HandleFunc("GET /healthz", handleHealth)
		mux.HandleFunc("GET /api/v1/version", handleStubVersion)
		mux.HandleFunc("GET /api/v1/vms", handleStubListVMs)
		mux.HandleFunc("POST /api/v1/vms", handleStubCreateVM)
		mux.HandleFunc("GET /api/v1/vms/{id}", handleStubGetVM)
		mux.HandleFunc("DELETE /api/v1/vms/{id}", handleStubDeleteVM)
		mux.HandleFunc("POST /api/v1/vms/{id}/start", handleStubVMAction)
		mux.HandleFunc("POST /api/v1/vms/{id}/stop", handleStubVMAction)
		mux.HandleFunc("POST /api/v1/vms/{id}/pause", handleStubVMAction)
		mux.HandleFunc("GET /api/v1/nodes", handleListNodes)
	}

	// Middleware chain: RequestID → Logging → CORS → Recovery
	var handler http.Handler = mux
	handler = recoveryMiddleware(handler)
	handler = corsMiddleware(handler)
	handler = loggingMiddleware(handler)
	handler = requestIDMiddleware(handler)

	return handler
}

// ── Middleware ────────────────────────────────────────────

var requestCounter atomic.Uint64

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cnt := requestCounter.Add(1)
		reqID := fmt.Sprintf("hcv-%d-%d", time.Now().UnixMilli(), cnt)
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC recovered: %v", err)
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── Live Handlers (backed by compute service) ────────────

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (svc *Services) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"product":        "HardCoreVisor",
		"version":        svc.Version.Version,
		"arch":           "hybrid-rust-go",
		"git_commit":     svc.Version.GitCommit,
		"build_date":     svc.Version.BuildDate,
		"vmcore_version": svc.Version.VMCore,
	})
}

func (svc *Services) handleListVMs(w http.ResponseWriter, _ *http.Request) {
	vms := svc.Compute.ListVMs()
	writeJSON(w, http.StatusOK, vms)
}

func (svc *Services) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		VCPUs    uint32 `json:"vcpus"`
		MemoryMB uint64 `json:"memory_mb"`
		Backend  string `json:"backend"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Name == "" {
		req.Name = "unnamed"
	}
	if req.VCPUs == 0 {
		req.VCPUs = 1
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = 512
	}

	vm, err := svc.Compute.CreateVM(req.Name, req.VCPUs, req.MemoryMB, req.Backend)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, vm)
}

func (svc *Services) handleGetVM(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	vm, err := svc.Compute.GetVM(handle)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

func (svc *Services) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := svc.Compute.DestroyVM(handle); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (svc *Services) handleVMAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle, err := parseVMID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		vm, err := svc.Compute.ActionVM(handle, action)
		if err != nil {
			// Check if it's a state transition error
			if isStateError(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, vm)
	}
}

func (svc *Services) handleListBackends(w http.ResponseWriter, _ *http.Request) {
	backends := svc.Compute.ListBackends()
	writeJSON(w, http.StatusOK, backends)
}

// ── Stub Handlers (no compute service) ───────────────────

type StubVMInfo struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	VCPUs    int    `json:"vcpus"`
	MemoryMB int    `json:"memory_mb"`
	Node     string `json:"node"`
}

var stubVMs = []StubVMInfo{
	{ID: 1, Name: "web-prod-01", State: "running", VCPUs: 4, MemoryMB: 8192, Node: "node-01"},
	{ID: 2, Name: "db-prod-01", State: "running", VCPUs: 8, MemoryMB: 32768, Node: "node-01"},
	{ID: 3, Name: "dev-test-01", State: "stopped", VCPUs: 2, MemoryMB: 4096, Node: "node-02"},
}

func handleStubVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"product": "HardCoreVisor",
		"version": "0.1.0",
		"arch":    "hybrid-rust-go",
	})
}

func handleStubListVMs(w http.ResponseWriter, _ *http.Request)  { writeJSON(w, http.StatusOK, stubVMs) }
func handleStubCreateVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		VCPUs    int    `json:"vcpus"`
		MemoryMB int    `json:"memory_mb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	vm := StubVMInfo{ID: len(stubVMs) + 1, Name: req.Name, State: "created", VCPUs: req.VCPUs, MemoryMB: req.MemoryMB, Node: "node-01"}
	stubVMs = append(stubVMs, vm)
	writeJSON(w, http.StatusCreated, vm)
}
func handleStubGetVM(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stub", "id": r.PathValue("id")})
}
func handleStubDeleteVM(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}
func handleStubVMAction(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"action": "stub", "id": r.PathValue("id")})
}

// ── Node/Cluster Handlers ────────────────────────────────

type NodeInfo struct {
	Name          string  `json:"name"`
	Status        string  `json:"status"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	VMCount       int     `json:"vm_count"`
}

func handleListNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := []NodeInfo{
		{Name: "node-01", Status: "online", CPUPercent: 45.2, MemoryPercent: 62.1, VMCount: 2},
		{Name: "node-02", Status: "online", CPUPercent: 38.7, MemoryPercent: 55.4, VMCount: 1},
		{Name: "node-03", Status: "online", CPUPercent: 78.3, MemoryPercent: 81.0, VMCount: 0},
	}
	writeJSON(w, http.StatusOK, nodes)
}

// ── Storage Handlers ─────────────────────────────────

func (svc *Services) handleStoragePools(w http.ResponseWriter, _ *http.Request) {
	if svc.Storage == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, svc.Storage.ListPools())
}

func (svc *Services) handleStorageVolumes(w http.ResponseWriter, r *http.Request) {
	pool := r.URL.Query().Get("pool")
	writeJSON(w, http.StatusOK, svc.Storage.ListVolumes(pool))
}

func (svc *Services) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pool      string `json:"pool"`
		Name      string `json:"name"`
		SizeBytes uint64 `json:"size_bytes"`
		Format    string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	vol, err := svc.Storage.CreateVolume(req.Pool, req.Name, req.Format, req.SizeBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, vol)
}

func (svc *Services) handleDeleteVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Storage.DeleteVolume(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Network Handlers ─────────────────────────────────

func (svc *Services) handleNetworkZones(w http.ResponseWriter, _ *http.Request) {
	if svc.Network == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, svc.Network.ListZones())
}

func (svc *Services) handleNetworkVNets(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("zone")
	writeJSON(w, http.StatusOK, svc.Network.ListVNets(zone))
}

func (svc *Services) handleFirewallRules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, svc.Network.ListFirewallRules())
}

// ── Peripheral Handlers ──────────────────────────────

func (svc *Services) handleListDevices(w http.ResponseWriter, r *http.Request) {
	typeFilter := peripheral.DeviceType(r.URL.Query().Get("type"))
	writeJSON(w, http.StatusOK, svc.Peripheral.ListDevices(typeFilter))
}

func (svc *Services) handleAttachDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	var req struct {
		VMHandle int32 `json:"vm_handle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := svc.Peripheral.AttachDevice(deviceID, req.VMHandle); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "attached"})
}

func (svc *Services) handleDetachDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	if err := svc.Peripheral.DetachDevice(deviceID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "detached"})
}

// ── HA / Cluster Handlers ────────────────────────────

func (svc *Services) handleClusterStatus(w http.ResponseWriter, _ *http.Request) {
	if svc.HA == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "healthy", "node_count": 3, "quorum": true,
		})
		return
	}
	writeJSON(w, http.StatusOK, svc.HA.GetClusterStatus())
}

func (svc *Services) handleClusterNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, svc.HA.ListNodes())
}

func (svc *Services) handleFenceNode(w http.ResponseWriter, r *http.Request) {
	nodeName := r.PathValue("node")
	var req struct {
		Reason string `json:"reason"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	event, err := svc.HA.FenceNode(nodeName, req.Reason, req.Action)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func handleStubList(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []map[string]string{
			{"name": name + "-default", "status": "active"},
		})
	}
}

// ── Helpers ──────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func parseVMID(r *http.Request) (int32, error) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid VM ID: %s", idStr)
	}
	return int32(id), nil
}

func isStateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if ffiErr, ok := err.(*ffi.FFIError); ok {
		return ffiErr.Code == ffi.ErrInvalidState
	}
	return strings.Contains(msg, "invalid state")
}
