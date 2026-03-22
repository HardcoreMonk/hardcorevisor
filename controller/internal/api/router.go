// Package api — REST API 게이트웨이 + 미들웨어 체인
//
// # 패키지 목적
//
// Go Controller의 REST API 엔드포인트와 미들웨어를 정의한다.
// Services 구조체를 통해 5개 백엔드 서비스(Compute, Storage, Network,
// Peripheral, HA)를 HTTP 핸들러에 연결한다.
//
// # 아키텍처 위치
//
//	HTTP 요청 → 미들웨어 체인 → HTTP 핸들러 → 서비스 레이어 → 백엔드
//
// # 사용된 패턴
//
//   - Services 기반 라우터: nil이면 스텁 모드, 값이 있으면 라이브 모드
//   - 미들웨어 체인: 데코레이터 패턴으로 HTTP 핸들러를 감싸서 횡단 관심사 처리
//   - 구조화된 에러 응답: APIError 타입으로 일관된 에러 형식 제공
package api

import (
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/auth"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/backup"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/image"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/metrics"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/snapshot"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/template"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

// VersionInfo — /api/v1/version에서 반환되는 버전 메타데이터.
type VersionInfo struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	VMCore    string `json:"vmcore_version"`
}

// Services — API 레이어가 사용하는 모든 백엔드 서비스를 집약한 구조체.
//
// 각 서비스가 nil이면 해당 기능의 엔드포인트가 등록되지 않는다.
// Compute는 ComputeProvider 인터페이스를 사용하여 ComputeService와
// PersistentComputeService를 투명하게 교체할 수 있다.
type Services struct {
	Compute    compute.ComputeProvider
	Storage    *storage.Service
	Network    *network.Service
	Peripheral *peripheral.Service
	HA         *ha.Service
	Backup     *backup.Service
	Template   *template.Service
	Snapshot   *snapshot.Service
	Image      *image.Service
	Version    VersionInfo
	EventHub   *EventHub
}

// NewRouter — 미들웨어가 적용된 HTTP 라우터를 생성한다.
//
// # 매개변수
//   - svc: 백엔드 서비스 집합. nil이면 스텁 핸들러 사용 (하위 호환성)
//   - rbacUsers: RBAC 사용자 맵. 비어 있거나 nil이면 RBAC 비활성화
//
// # 미들웨어 체인 (바깥쪽부터 적용 순서)
//
//  1. RequestID: 모든 요청에 X-Request-Id 헤더 부여 (추적용)
//  2. Version: X-API-Version 헤더 부여 + 폐기 경로 감지
//  3. Audit: 모든 API 호출을 구조화 JSON으로 감사 로깅
//  4. Logging: 요청 처리 시간 기록
//  5. Metrics: Prometheus 메트릭 수집 (요청 수, 레이턴시)
//  6. RBAC: Basic Auth 기반 역할 검증 (admin/operator/viewer)
//  7. CORS: Cross-Origin 요청 허용 + OPTIONS preflight 처리
//  8. RateLimit: 토큰 버킷 속도 제한 (HCV_RATE_LIMIT 환경변수)
//  9. Recovery: 패닉 복구 → 500 응답 반환 (서버 안정성 보장)
//
// # 반환값
//
// 미들웨어가 적용된 http.Handler
var metricsRegistered sync.Once

func NewRouter(svc *Services, rbacUsers ...map[string]auth.RBACUser) http.Handler {
	metricsRegistered.Do(metrics.Register)
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
		mux.HandleFunc("POST /api/v1/vms/{id}/migrate", svc.handleMigrateVM)
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

		// Backup
		if svc.Backup != nil {
			mux.HandleFunc("GET /api/v1/backups", svc.handleListBackups)
			mux.HandleFunc("POST /api/v1/backups", svc.handleCreateBackup)
			mux.HandleFunc("GET /api/v1/backups/{id}", svc.handleGetBackup)
			mux.HandleFunc("DELETE /api/v1/backups/{id}", svc.handleDeleteBackup)
		}

		// Template
		if svc.Template != nil {
			mux.HandleFunc("GET /api/v1/templates", svc.handleListTemplates)
			mux.HandleFunc("POST /api/v1/templates", svc.handleCreateTemplate)
			mux.HandleFunc("GET /api/v1/templates/{id}", svc.handleGetTemplate)
			mux.HandleFunc("DELETE /api/v1/templates/{id}", svc.handleDeleteTemplate)
			mux.HandleFunc("POST /api/v1/templates/{id}/deploy", svc.handleDeployTemplate)
		}

		// Snapshot
		if svc.Snapshot != nil {
			mux.HandleFunc("GET /api/v1/snapshots", svc.handleListSnapshots)
			mux.HandleFunc("POST /api/v1/snapshots", svc.handleCreateSnapshot)
			mux.HandleFunc("GET /api/v1/snapshots/{id}", svc.handleGetSnapshot)
			mux.HandleFunc("DELETE /api/v1/snapshots/{id}", svc.handleDeleteSnapshot)
			mux.HandleFunc("POST /api/v1/snapshots/{id}/restore", svc.handleRestoreSnapshot)
		}

		// Image Registry
		if svc.Image != nil {
			mux.HandleFunc("GET /api/v1/images", svc.handleListImages)
			mux.HandleFunc("POST /api/v1/images", svc.handleRegisterImage)
			mux.HandleFunc("GET /api/v1/images/{id}", svc.handleGetImage)
			mux.HandleFunc("DELETE /api/v1/images/{id}", svc.handleDeleteImage)
		}

		// Webhook
		mux.HandleFunc("POST /api/v1/webhooks/alert", handleAlertWebhook)

		// System Stats
		mux.HandleFunc("GET /api/v1/system/stats", svc.handleSystemStats)

		// API Info
		mux.HandleFunc("GET /api/v1/api-info", handleAPIInfo)

		// WebSocket
		if svc.EventHub != nil {
			mux.HandleFunc("GET /ws", svc.EventHub.HandleWS)
		}
	}

	// Metrics endpoint (available in both live and stub modes)
	metricsHandler := promhttp.Handler()
	if svc != nil {
		mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
			metrics.CollectFromServices(&metrics.ServiceRefs{
				Compute: svc.Compute,
				Storage: svc.Storage,
				HA:      svc.HA,
			})
			metricsHandler.ServeHTTP(w, r)
		})
	} else {
		mux.Handle("GET /metrics", metricsHandler)
	}

	if svc == nil {
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

		// Webhook
		mux.HandleFunc("POST /api/v1/webhooks/alert", handleAlertWebhook)

		// API Info
		mux.HandleFunc("GET /api/v1/api-info", handleAPIInfo)
	}

	// 미들웨어 체인 구성 (안쪽부터 바깥쪽으로 감싸는 순서)
	// 실행 순서는 반대: RequestID → Version → Audit → Logging → Metrics → RBAC → CORS → RateLimit → Recovery → Handler
	var handler http.Handler = mux
	// 9. Recovery: 핸들러 패닉 시 500 응답 반환, 서버 크래시 방지
	handler = recoveryMiddleware(handler)

	// 8. RateLimit: 토큰 버킷 속도 제한 (HCV_RATE_LIMIT 환경변수, 초당 요청 수)
	if rlStr := os.Getenv("HCV_RATE_LIMIT"); rlStr != "" {
		if rate, err := strconv.Atoi(rlStr); err == nil && rate > 0 {
			limiter := NewRateLimiter(float64(rate), rate)
			handler = RateLimitMiddleware(limiter)(handler)
		}
	}

	// 7. CORS: Cross-Origin 요청 허용 + OPTIONS preflight 처리
	handler = corsMiddleware(handler)

	// 6. RBAC: Basic Auth 기반 역할 검증 (설정된 경우만 활성화)
	if len(rbacUsers) > 0 && rbacUsers[0] != nil {
		handler = auth.RBACMiddleware(rbacUsers[0])(handler)
	}

	// 5. Metrics: Prometheus 메트릭 수집 (hcv_api_requests_total 등)
	handler = metrics.InstrumentHandler(handler)
	// 4. Logging: 요청 처리 시간을 로그에 기록
	handler = loggingMiddleware(handler)

	// 3. Audit: 모든 API 호출을 구조화 JSON으로 감사 로깅 (항상 활성)
	auditLogger := auth.NewAuditLogger()
	handler = auth.AuditMiddleware(auditLogger)(handler)

	// 2. Version: X-API-Version 헤더 부여 + 폐기 경로 감지
	handler = versionMiddleware(handler)
	// 1. RequestID: 모든 요청에 고유 X-Request-Id 부여 (추적용)
	handler = requestIDMiddleware(handler)

	return handler
}

// ── 미들웨어 ────────────────────────────────────────────

var requestCounter atomic.Uint64

// requestIDMiddleware — 모든 요청에 고유한 X-Request-Id 헤더를 부여한다.
// 형식: "hcv-{밀리초타임스탬프}-{순차카운터}"
// 로그와 에러 추적에 사용된다.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cnt := requestCounter.Add(1)
		reqID := fmt.Sprintf("hcv-%d-%d", time.Now().UnixMilli(), cnt)
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware — 요청 처리 시간을 로그에 기록한다.
// 출력 형식: "원격주소 메서드 경로 소요시간"
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %v", r.RemoteAddr, r.Method, r.URL.Path, time.Since(start))
	})
}

// corsMiddleware — CORS(Cross-Origin Resource Sharing) 헤더를 설정한다.
// 모든 출처(*)를 허용하며, OPTIONS preflight 요청에 204를 반환한다.
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

// recoveryMiddleware — 핸들러에서 발생한 패닉을 복구하여 서버 크래시를 방지한다.
// 패닉 발생 시 500 Internal Server Error를 JSON 형식으로 반환한다.
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

// ── 라이브 핸들러 (Compute 서비스 기반) ────────────

// handleHealth — 헬스 체크 엔드포인트 (GET /healthz).
// 항상 {"status":"ok"}을 반환한다. 로드밸런서/모니터링에서 사용한다.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVersion — 버전 정보를 반환한다 (GET /api/v1/version).
// 제품명, 버전, 아키텍처, git 커밋, 빌드 날짜, vmcore 버전을 포함한다.
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

// handleListVMs — VM 목록을 반환한다 (GET /api/v1/vms).
// offset/limit 쿼리 파라미터로 페이지네이션을 지원한다.
func (svc *Services) handleListVMs(w http.ResponseWriter, r *http.Request) {
	vms := svc.Compute.ListVMs()
	offset, limit := parsePagination(r)
	total := len(vms)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, paginate(vms[offset:end], total, offset, limit))
}

// handleCreateVM — 새 VM을 생성한다 (POST /api/v1/vms).
// 요청 본문: {"name": "...", "vcpus": N, "memory_mb": N, "backend": "rustvmm|qemu"}
// backend가 비어 있으면 BackendSelector가 자동 선택한다.
// 성공 시 201 Created + 생성된 VM 정보를 반환한다.
func (svc *Services) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		VCPUs    uint32 `json:"vcpus"`
		MemoryMB uint64 `json:"memory_mb"`
		Backend  string `json:"backend"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
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
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, vm)
}

func (svc *Services) handleGetVM(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	vm, err := svc.Compute.GetVM(handle)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vm)
}

func (svc *Services) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	if err := svc.Compute.DestroyVM(handle); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVMAction — VM 생명주기 액션을 수행하는 핸들러를 생성한다.
// action: "start", "stop", "pause", "resume"
// 잘못된 상태 전이 시 409 Conflict를 반환한다 (예: stopped → pause).
func (svc *Services) handleVMAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle, err := parseVMID(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
			return
		}
		vm, err := svc.Compute.ActionVM(handle, action)
		if err != nil {
			// Check if it's a state transition error
			if isStateError(err) {
				writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
				return
			}
			writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, vm)
	}
}

func (svc *Services) handleMigrateVM(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var req struct {
		TargetNode string `json:"target_node"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.TargetNode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_node is required"})
		return
	}
	if err := svc.Compute.MigrateVM(handle, req.TargetNode); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "migrated",
		"message": fmt.Sprintf("VM %d migrated to %s", handle, req.TargetNode),
	})
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
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if msg := validateRequired(map[string]string{"pool": req.Pool, "name": req.Name}); msg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, msg)
		return
	}
	vol, err := svc.Storage.CreateVolume(req.Pool, req.Name, req.Format, req.SizeBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
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

// ── Backup Handlers ──────────────────────────────────

func (svc *Services) handleListBackups(w http.ResponseWriter, r *http.Request) {
	backups := svc.Backup.ListBackups()
	offset, limit := parsePagination(r)
	total := len(backups)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, paginate(backups[offset:end], total, offset, limit))
}

func (svc *Services) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VMID   int32  `json:"vm_id"`
		VMName string `json:"vm_name"`
		Pool   string `json:"pool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if msg := validateRequired(map[string]string{"vm_name": req.VMName, "pool": req.Pool}); msg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, msg)
		return
	}
	b, err := svc.Backup.CreateBackup(req.VMID, req.VMName, req.Pool)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (svc *Services) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := svc.Backup.GetBackup(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (svc *Services) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Backup.DeleteBackup(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Template Handlers ────────────────────────────────

func (svc *Services) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates := svc.Template.List()
	offset, limit := parsePagination(r)
	total := len(templates)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, paginate(templates[offset:end], total, offset, limit))
}

func (svc *Services) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		VCPUs       uint32 `json:"vcpus"`
		MemoryMB    uint64 `json:"memory_mb"`
		DiskSizeGB  uint64 `json:"disk_size_gb"`
		Backend     string `json:"backend"`
		OSType      string `json:"os_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
		return
	}
	t, err := svc.Template.Create(req.Name, req.Description, req.VCPUs, req.MemoryMB, req.DiskSizeGB, req.Backend, req.OSType)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (svc *Services) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := svc.Template.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (svc *Services) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Template.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (svc *Services) handleDeployTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := svc.Template.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Name == "" {
		req.Name = t.Name + "-vm"
	}
	vm, err := svc.Compute.CreateVM(req.Name, t.VCPUs, t.MemoryMB, t.Backend)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, vm)
}

// ── Snapshot Handlers ─────────────────────────────────

func (svc *Services) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	var vmID int32
	if idStr := r.URL.Query().Get("vm_id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid vm_id")
			return
		}
		vmID = int32(id)
	}
	writeJSON(w, http.StatusOK, svc.Snapshot.List(vmID))
}

func (svc *Services) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VMID   int32  `json:"vm_id"`
		VMName string `json:"vm_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.VMName == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "vm_name is required")
		return
	}
	snap, err := svc.Snapshot.Create(req.VMID, req.VMName)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (svc *Services) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, err := svc.Snapshot.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (svc *Services) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Snapshot.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (svc *Services) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, err := svc.Snapshot.Restore(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// ── Image Handlers ────────────────────────────────────

func (svc *Services) handleListImages(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, svc.Image.List())
}

func (svc *Services) handleRegisterImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Format string `json:"format"`
		Path   string `json:"path"`
		OSType string `json:"os_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if msg := validateRequired(map[string]string{"name": req.Name, "format": req.Format}); msg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, msg)
		return
	}
	img, err := svc.Image.Register(req.Name, req.Format, req.Path, req.OSType)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, img)
}

func (svc *Services) handleGetImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	img, err := svc.Image.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, img)
}

func (svc *Services) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Image.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Webhook Handlers ─────────────────────────────────

// AlertmanagerAlert represents a single alert from Alertmanager webhook payload.
type AlertmanagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// AlertmanagerWebhook represents the full Alertmanager webhook payload.
type AlertmanagerWebhook struct {
	Version           string              `json:"version"`
	GroupKey           string              `json:"groupKey"`
	TruncatedAlerts   int                 `json:"truncatedAlerts"`
	Status            string              `json:"status"`
	Receiver          string              `json:"receiver"`
	GroupLabels       map[string]string   `json:"groupLabels"`
	CommonLabels      map[string]string   `json:"commonLabels"`
	CommonAnnotations map[string]string   `json:"commonAnnotations"`
	ExternalURL       string              `json:"externalURL"`
	Alerts            []AlertmanagerAlert `json:"alerts"`
}

func handleAlertWebhook(w http.ResponseWriter, r *http.Request) {
	var payload AlertmanagerWebhook
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	for _, alert := range payload.Alerts {
		slog.Warn("alert received",
			slog.String("status", alert.Status),
			slog.String("alertname", alert.Labels["alertname"]),
			slog.String("severity", alert.Labels["severity"]),
			slog.String("summary", alert.Annotations["summary"]),
			slog.String("fingerprint", alert.Fingerprint),
		)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "received"})
}

// ── API Info Handler ─────────────────────────────────

// APIInfo holds API version metadata.
type APIInfo struct {
	CurrentVersion     string   `json:"current_version"`
	SupportedVersions  []string `json:"supported_versions"`
	DeprecatedVersions []string `json:"deprecated_versions"`
	DocsURL            string   `json:"docs_url"`
}

func handleAPIInfo(w http.ResponseWriter, _ *http.Request) {
	info := APIInfo{
		CurrentVersion:     CurrentAPIVersion,
		SupportedVersions:  []string{"v1"},
		DeprecatedVersions: []string{},
		DocsURL:            "/docs/openapi.yaml",
	}
	writeJSON(w, http.StatusOK, info)
}

// ── System Stats Handler ─────────────────────────────

var startTime = time.Now()

func (svc *Services) handleSystemStats(w http.ResponseWriter, _ *http.Request) {
	stats := map[string]any{
		"version":        svc.Version,
		"uptime_seconds": time.Since(startTime).Seconds(),
		"vms": map[string]any{
			"total":    len(svc.Compute.ListVMs()),
			"by_state": countVMsByState(svc.Compute.ListVMs()),
		},
		"storage": map[string]any{
			"pools":   len(svc.Storage.ListPools()),
			"volumes": len(svc.Storage.ListVolumes("")),
		},
		"network": map[string]any{
			"zones":          len(svc.Network.ListZones()),
			"vnets":          len(svc.Network.ListVNets("")),
			"firewall_rules": len(svc.Network.ListFirewallRules()),
		},
		"cluster": svc.HA.GetClusterStatus(),
		"devices": len(svc.Peripheral.ListDevices("")),
		"backups": len(svc.Backup.ListBackups()),
	}
	writeJSON(w, http.StatusOK, stats)
}

func countVMsByState(vms []*compute.VMInfo) map[string]int {
	counts := make(map[string]int)
	for _, vm := range vms {
		counts[vm.State]++
	}
	return counts
}

// ── 헬퍼 함수 ──────────────────────────────────────────────

// writeJSON — JSON 응답을 작성한다.
// Content-Type을 application/json으로 설정하고 data를 JSON으로 인코딩한다.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// parseVMID — URL 경로에서 VM ID를 추출하여 int32로 변환한다.
// 유효하지 않은 ID 형식이면 에러를 반환한다.
func parseVMID(r *http.Request) (int32, error) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid VM ID: %s", idStr)
	}
	return int32(id), nil
}

// isStateError — 에러가 VM 상태 전이 에러인지 판별한다.
// FFIError의 코드가 ErrInvalidState이거나, 메시지에 "invalid state"가 포함되면 true.
// 409 Conflict 응답을 반환할지 결정하는 데 사용된다.
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
