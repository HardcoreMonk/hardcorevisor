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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
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
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/task"
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

// AuthServices — JWT 인증 서비스와 사용자 DB를 집약한 구조체.
//
// 필드가 nil이면 인증 관련 엔드포인트(/api/v1/auth/*)가 등록되지 않는다.
// Controller main.go에서 UserDB 초기화 성공 시에만 생성된다.
type AuthServices struct {
	UserDB     *auth.UserDB
	JWTService *auth.JWTService
}

// HAServices 는 HA 프로덕션 기능 (리더 선출, 분산 잠금, 장애 복구)을 집약한다.
// 모든 필드는 nil 허용이며, nil인 경우 해당 기능이 비활성화된다.
type HAServices struct {
	LeaderElection  *ha.LeaderElection
	LockManager     *ha.LockManager
	FailoverManager *ha.FailoverManager
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
	LXC        *compute.LXCBackend
	Task       *task.TaskService
	Auth       *AuthServices
	OAuth2     *auth.OAuth2Provider
	HAServices *HAServices
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
		mux.HandleFunc("GET /readyz", svc.handleReadiness)
		mux.HandleFunc("GET /startupz", handleHealth)
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
		mux.HandleFunc("GET /api/v1/vms/{id}/migration", svc.handleGetMigrationStatus)
		mux.HandleFunc("DELETE /api/v1/vms/{id}/migration", svc.handleCancelMigration)
		mux.HandleFunc("GET /api/v1/vms/{id}/stats", svc.handleVMStats)
		mux.HandleFunc("POST /api/v1/vms/{id}/exec", svc.handleContainerExec)
		mux.HandleFunc("GET /api/v1/nodes", handleListNodes)
		mux.HandleFunc("GET /api/v1/nodes/{id}", svc.handleGetNode)

		// Tasks
		if svc.Task != nil {
			mux.HandleFunc("GET /api/v1/tasks", svc.handleListTasks)
			mux.HandleFunc("GET /api/v1/tasks/{id}", svc.handleGetTask)
			mux.HandleFunc("DELETE /api/v1/tasks/{id}", svc.handleDeleteTask)
		}
		mux.HandleFunc("GET /api/v1/backends", svc.handleListBackends)
		mux.HandleFunc("GET /api/v1/templates/lxc", svc.handleLXCTemplates)

		// Storage
		mux.HandleFunc("GET /api/v1/storage/pools", svc.handleStoragePools)
		mux.HandleFunc("GET /api/v1/storage/volumes", svc.handleStorageVolumes)
		mux.HandleFunc("POST /api/v1/storage/volumes", svc.handleCreateVolume)
		mux.HandleFunc("GET /api/v1/storage/volumes/{id}", svc.handleGetVolume)
		mux.HandleFunc("DELETE /api/v1/storage/volumes/{id}", svc.handleDeleteVolume)
		mux.HandleFunc("POST /api/v1/storage/volumes/{id}/resize", svc.handleResizeVolume)
		mux.HandleFunc("POST /api/v1/storage/volumes/{id}/export", svc.handleExportVolume)
		mux.HandleFunc("POST /api/v1/storage/volumes/import", svc.handleImportVolume)

		// Storage Snapshots
		mux.HandleFunc("POST /api/v1/storage/snapshots/{id}/rollback", svc.handleStorageSnapshotRollback)
		mux.HandleFunc("POST /api/v1/storage/snapshots/{id}/clone", svc.handleStorageSnapshotClone)
		mux.HandleFunc("DELETE /api/v1/storage/snapshots/{id}", svc.handleDeleteStorageSnapshot)

		// Network
		mux.HandleFunc("GET /api/v1/network/zones", svc.handleNetworkZones)
		mux.HandleFunc("POST /api/v1/network/zones", svc.handleCreateZone)
		mux.HandleFunc("DELETE /api/v1/network/zones/{name}", svc.handleDeleteZone)
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
		mux.HandleFunc("GET /api/v1/cluster/leader", svc.handleClusterLeader)
		mux.HandleFunc("POST /api/v1/cluster/promote", svc.handleClusterPromote)

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

		// Auth endpoints (JWT login/refresh/logout + user management)
		if svc.Auth != nil {
			mux.HandleFunc("POST /api/v1/auth/login", svc.handleAuthLogin)
			mux.HandleFunc("POST /api/v1/auth/refresh", svc.handleAuthRefresh)
			mux.HandleFunc("POST /api/v1/auth/logout", svc.handleAuthLogout)
			mux.HandleFunc("GET /api/v1/auth/users", svc.handleAuthListUsers)
			mux.HandleFunc("POST /api/v1/auth/users", svc.handleAuthCreateUser)
			mux.HandleFunc("DELETE /api/v1/auth/users/{username}", svc.handleAuthDeleteUser)
		}

		// OAuth2/OIDC endpoints
		if svc.OAuth2 != nil {
			mux.HandleFunc("GET /api/v1/auth/oauth2/providers", svc.handleOAuth2Providers)
			mux.HandleFunc("GET /api/v1/auth/oauth2/authorize", svc.handleOAuth2Authorize)
			mux.HandleFunc("GET /api/v1/auth/oauth2/callback", svc.handleOAuth2Callback)
		}
	}

	// Swagger UI (available in both live and stub modes, no auth required)
	mux.HandleFunc("GET /api/v1/docs", handleSwaggerUI)
	mux.HandleFunc("GET /api/v1/docs/openapi.yaml", handleOpenAPISpec)

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

	// 7.5 Security Headers: X-Content-Type-Options, X-Frame-Options, X-XSS-Protection
	handler = auth.SecurityHeadersMiddleware(handler)

	// 7. CORS: Cross-Origin 요청 허용 + OPTIONS preflight 처리
	handler = corsMiddleware(handler)

	// 6. RBAC: JWT Bearer / Basic Auth / Legacy 역할 검증 (설정된 경우만 활성화)
	if len(rbacUsers) > 0 && rbacUsers[0] != nil {
		var rbacCfg *auth.RBACConfig
		if svc != nil && svc.Auth != nil {
			rbacCfg = &auth.RBACConfig{
				JWTService: svc.Auth.JWTService,
				UserDB:     svc.Auth.UserDB,
			}
		}
		handler = auth.RBACMiddleware(rbacUsers[0], rbacCfg)(handler)
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
		slog.Info("http request", "addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
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
				stack := make([]byte, 4096)
				n := runtime.Stack(stack, false)
				slog.Error("panic recovered", "panic", err, "stack", string(stack[:n]))
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── 라이브 핸들러 (Compute 서비스 기반) ────────────

// handleHealth — 라이브니스 프로브 (GET /healthz).
// 프로세스가 살아 있으면 200을 반환한다. 의존성 상태는 확인하지 않는다.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadiness — 레디니스 프로브 (GET /readyz).
// 서비스가 트래픽을 받을 준비가 되었는지 확인한다.
// Compute, Storage, HA 서비스의 기본 동작 여부를 검증한다.
func (svc *Services) handleReadiness(w http.ResponseWriter, _ *http.Request) {
	checks := map[string]string{}
	allOk := true

	// Compute 서비스 확인
	if svc.Compute != nil {
		checks["compute"] = "ok"
	} else {
		checks["compute"] = "unavailable"
		allOk = false
	}

	// Storage 서비스 확인
	if svc.Storage != nil {
		checks["storage"] = "ok"
	} else {
		checks["storage"] = "unavailable"
		allOk = false
	}

	// HA 서비스 확인
	if svc.HA != nil {
		checks["ha"] = "ok"
	} else {
		checks["ha"] = "unavailable"
	}

	if allOk {
		checks["status"] = "ready"
		writeJSON(w, http.StatusOK, checks)
	} else {
		checks["status"] = "not_ready"
		writeJSON(w, http.StatusServiceUnavailable, checks)
	}
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
// type 쿼리 파라미터로 "vm" 또는 "container"를 필터링할 수 있다.
func (svc *Services) handleListVMs(w http.ResponseWriter, r *http.Request) {
	vms := svc.Compute.ListVMs()

	// Filter by type if specified
	if typeFilter := r.URL.Query().Get("type"); typeFilter != "" {
		filtered := make([]*compute.VMInfo, 0)
		for _, vm := range vms {
			vmType := vm.Type
			if vmType == "" {
				vmType = "vm" // default for backward compat
			}
			if vmType == typeFilter {
				filtered = append(filtered, vm)
			}
		}
		vms = filtered
	}

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
// 요청 본문: {"name": "...", "vcpus": N, "memory_mb": N, "backend": "rustvmm|qemu|lxc", "type": "vm|container", "template": "ubuntu|alpine|..."}
// type이 "container"이면 lxc 백엔드를 자동 선택한다.
// backend가 비어 있으면 BackendSelector가 자동 선택한다.
// 성공 시 201 Created + 생성된 VM 정보를 반환한다.
func (svc *Services) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string `json:"name"`
		VCPUs         uint32 `json:"vcpus"`
		MemoryMB      uint64 `json:"memory_mb"`
		Backend       string `json:"backend"`
		Type          string `json:"type"`
		Template      string `json:"template"`
		RestartPolicy string `json:"restart_policy"`
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
	// 리소스 상한 검증 — DoS 및 잘못된 설정 방지
	if req.VCPUs > 256 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "vcpus must be <= 256")
		return
	}
	if req.MemoryMB > 1048576 { // 1TB
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "memory_mb must be <= 1048576 (1TB)")
		return
	}

	// If type is "container", auto-select lxc backend
	backendHint := req.Backend
	if req.Type == "container" && backendHint == "" {
		backendHint = "lxc"
	}

	vm, err := svc.Compute.CreateVM(req.Name, req.VCPUs, req.MemoryMB, backendHint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	// Set restart policy if specified (default is "always")
	if req.RestartPolicy != "" {
		vm.RestartPolicy = req.RestartPolicy
	}

	// Set template if specified (for LXC containers)
	if req.Template != "" {
		vm.Template = req.Template
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

// handleMigrateVM — VM 라이브 마이그레이션을 요청한다 (POST /api/v1/vms/{id}/migrate).
//
// 요청 본문: {"target_node": "node-02"}
//
// 동작 모드:
//  1. TaskService가 있는 경우 (비동기): MigrateLive() + 태스크 추적 goroutine 생성
//     → 202 Accepted {"task_id":"task-1", "status":"pending", "message":"..."}
//     → 진행 상태는 GET /api/v1/tasks/{task_id} 로 폴링
//  2. TaskService가 없는 경우 (동기 폴백): MigrateVM()으로 완료까지 대기
//     → 200 OK {"status":"migrated", "message":"..."}
//
// 에러 응답: 400 (잘못된 요청), 404 (VM 미존재/상태 오류)
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

	// TaskService가 있으면 비동기 마이그레이션 + 태스크 추적 사용
	if svc.Task != nil {
		if err := svc.Compute.MigrateLive(handle, req.TargetNode); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		t := svc.Task.CreateTask("vm.migrate", handle)
		// Track migration progress in a goroutine with timeout
		go func() {
			t.SetStatus("running")
			deadline := time.After(30 * time.Minute) // 마이그레이션 최대 30분
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-deadline:
					t.Fail("migration timeout (30 minutes)")
					return
				case <-ticker.C:
					ms, err := svc.Compute.GetMigrationStatus(handle)
					if err != nil {
						t.Fail(err.Error())
						return
					}
					t.SetProgress(ms.Progress)
					switch ms.Phase {
					case "completed":
						t.Complete()
						return
					case "failed":
						t.Fail(ms.Error)
						return
					}
				}
			}
		}()
		snap := t.Snapshot()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"task_id": snap.ID,
			"status":  "pending",
			"message": fmt.Sprintf("VM %d migration to %s initiated", handle, req.TargetNode),
		})
		return
	}

	// Fallback: synchronous migration (no task service)
	if err := svc.Compute.MigrateVM(handle, req.TargetNode); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "migrated",
		"message": fmt.Sprintf("VM %d migrated to %s", handle, req.TargetNode),
	})
}

// handleCancelMigration — 진행 중인 비동기 마이그레이션을 취소한다.
//
// DELETE /api/v1/vms/{id}/migration
//
// ComputeProvider.CancelMigration()을 호출하여 마이그레이션 goroutine의
// context를 취소한다. 마이그레이션이 없으면 404를 반환한다.
//
// 성공 응답: 200 {"status":"cancelled", "message":"..."}
// 에러 응답: 400 (잘못된 VM ID), 404 (마이그레이션 없음)
func (svc *Services) handleCancelMigration(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	if err := svc.Compute.CancelMigration(handle); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "cancelled",
		"message": fmt.Sprintf("Migration for VM %d cancelled", handle),
	})
}

func (svc *Services) handleListBackends(w http.ResponseWriter, _ *http.Request) {
	backends := svc.Compute.ListBackends()
	writeJSON(w, http.StatusOK, backends)
}

// handleGetMigrationStatus — VM 마이그레이션 상태를 조회한다 (GET /api/v1/vms/{id}/migration).
func (svc *Services) handleGetMigrationStatus(w http.ResponseWriter, r *http.Request) {
	handle, err := parseVMID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	status, err := svc.Compute.GetMigrationStatus(handle)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleVMStats — VM/컨테이너 리소스 사용량 통계를 반환한다 (GET /api/v1/vms/{id}/stats).
// LXC 컨테이너의 경우 cgroup v2 기반 통계를, VM의 경우 플레이스홀더를 반환한다.
func (svc *Services) handleVMStats(w http.ResponseWriter, r *http.Request) {
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

	// LXC container stats
	if vm.Backend == "lxc" && svc.LXC != nil {
		stats, err := svc.LXC.GetContainerStats(handle)
		if err != nil {
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, stats)
		return
	}

	// VM stats placeholder
	writeJSON(w, http.StatusOK, &compute.ContainerStats{
		CPUUsageNs:       int64(vm.VCPUs) * 1_000_000_000,
		MemoryUsageBytes: int64(vm.MemoryMB) * 512 * 1024,
		MemoryLimitBytes: int64(vm.MemoryMB) * 1024 * 1024,
		PIDCount:         1,
		NetRxBytes:       0,
		NetTxBytes:       0,
	})
}

// handleContainerExec — 컨테이너 내에서 명령을 실행한다 (POST /api/v1/vms/{id}/exec).
// 요청 본문: {"command": ["ls", "-la"]}
// 응답: {"output": "..."} 또는 에러
// LXC 컨테이너만 지원한다.
func (svc *Services) handleContainerExec(w http.ResponseWriter, r *http.Request) {
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

	if vm.Type != "container" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "exec is only supported for containers")
		return
	}

	var req struct {
		Command []string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "command is required")
		return
	}

	if svc.LXC == nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "LXC backend not available")
		return
	}

	output, err := svc.LXC.ExecContainer(handle, req.Command)
	if err != nil {
		if isStateError(err) {
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"output": output})
}

// handleLXCTemplates — 사용 가능한 LXC 배포 템플릿 목록을 반환한다 (GET /api/v1/templates/lxc).
func (svc *Services) handleLXCTemplates(w http.ResponseWriter, _ *http.Request) {
	templates := []string{"ubuntu", "alpine", "debian", "centos"}
	if svc.LXC != nil {
		templates = svc.LXC.AvailableTemplates()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"templates": templates,
	})
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

// handleGetNode — 이름으로 특정 클러스터 노드를 조회한다 (GET /api/v1/nodes/{id}).
//
// 경로 파라미터: id — 노드 이름 (예: "node-01")
// 응답: 200 OK + ClusterNode JSON
// 에러 응답: 400 (이름 누락), 404 (HA 서비스 없음 또는 노드 미존재)
// HA 서비스의 GetNode()에 위임하여 노드 정보를 조회한다.
func (svc *Services) handleGetNode(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("id")
	if name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "node name is required")
		return
	}
	if svc.HA == nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "HA service not available")
		return
	}
	node, err := svc.HA.GetNode(name)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// ── Task Handlers ───────────────────────────────────

// handleListTasks — 태스크 목록을 조회한다 (GET /api/v1/tasks).
//
// 쿼리 파라미터:
//   - type: 태스크 유형 필터 (예: "vm.migrate", 빈 문자열이면 전체)
//   - status: 상태 필터 (예: "running", 빈 문자열이면 전체)
//
// 응답: 200 OK + TaskSnapshot 배열 JSON
// 주의: Task 포인터를 직접 직렬화하면 race condition이 발생할 수 있으므로,
// 반드시 Snapshot()으로 값 복사본을 생성한 후 직렬화한다.
func (svc *Services) handleListTasks(w http.ResponseWriter, r *http.Request) {
	typeFilter := r.URL.Query().Get("type")
	statusFilter := r.URL.Query().Get("status")
	tasks := svc.Task.ListTasks(typeFilter, statusFilter)
	// Snapshot으로 값 복사하여 JSON 직렬화 시 race condition 방지
	result := make([]task.TaskSnapshot, 0, len(tasks))
	for _, t := range tasks {
		result = append(result, t.Snapshot())
	}
	writeJSON(w, http.StatusOK, result)
}

// handleGetTask — ID로 태스크를 조회한다 (GET /api/v1/tasks/{id}).
//
// 경로 파라미터: id — 태스크 ID (예: "task-1")
// 응답: 200 OK + TaskSnapshot JSON, 또는 404 (태스크 미존재)
// Snapshot()으로 값 복사하여 race condition 방지.
func (svc *Services) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := svc.Task.GetTask(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	snap := t.Snapshot()
	writeJSON(w, http.StatusOK, &snap)
}

// handleDeleteTask — 완료/실패 태스크를 삭제한다 (DELETE /api/v1/tasks/{id}).
//
// 경로 파라미터: id — 삭제할 태스크 ID (예: "task-1")
// 성공 응답: 204 No Content
// 에러 응답: 400 (태스크 미존재, 또는 진행 중 삭제 시도)
// 주의: pending/running 상태의 태스크는 삭제할 수 없다.
func (svc *Services) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Task.DeleteTask(id); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// handleGetVolume — 볼륨 상세 조회 (GET /api/v1/storage/volumes/{id}).
// 볼륨 ID로 단일 볼륨 정보를 반환한다. 미존재 시 404.
func (svc *Services) handleGetVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	vol, err := svc.Storage.GetVolume(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vol)
}

// handleResizeVolume — 볼륨 크기 변경 (POST /api/v1/storage/volumes/{id}/resize).
// 요청 본문: {"size_bytes": N}. 볼륨 크기를 변경한다.
func (svc *Services) handleResizeVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		SizeBytes uint64 `json:"size_bytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.SizeBytes == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "size_bytes must be greater than 0")
		return
	}
	if err := svc.Storage.ResizeVolume(id, req.SizeBytes); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	// 변경된 볼륨 정보 반환
	vol, err := svc.Storage.GetVolume(id)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, vol)
}

// handleExportVolume — 볼륨 내보내기 (POST /api/v1/storage/volumes/{id}/export).
// 요청 본문: {"path": "/backup/disk.img"}. 볼륨을 파일로 내보낸다.
func (svc *Services) handleExportVolume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}
	if err := svc.Storage.ExportVolume(id, req.Path); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "exported", "volume_id": id, "path": req.Path})
}

// handleImportVolume — 볼륨 가져오기 (POST /api/v1/storage/volumes/import).
// 요청 본문: {"path": "/backup/disk.img", "pool": "local-zfs", "name": "imported-vol"}.
func (svc *Services) handleImportVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Pool string `json:"pool"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if msg := validateRequired(map[string]string{"path": req.Path, "pool": req.Pool, "name": req.Name}); msg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, msg)
		return
	}
	vol, err := svc.Storage.ImportVolume(req.Path, req.Pool, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, vol)
}

// ── OAuth2/OIDC Handlers ──────────────────────────────

// handleOAuth2Providers — 설정된 OAuth2 프로바이더 목록 (GET /api/v1/auth/oauth2/providers).
func (svc *Services) handleOAuth2Providers(w http.ResponseWriter, _ *http.Request) {
	cfg := svc.OAuth2.Config()
	provider := map[string]any{
		"name":         svc.OAuth2.ProviderName(),
		"provider_url": cfg.ProviderURL,
		"client_id":    cfg.ClientID,
		"redirect_url": cfg.RedirectURL,
		"scopes":       cfg.Scopes,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": []any{provider},
	})
}

// handleOAuth2Authorize — OAuth2 인증 URL 반환 (GET /api/v1/auth/oauth2/authorize).
// 쿼리 파라미터: state (CSRF 방지용). 없으면 자동 생성.
func (svc *Services) handleOAuth2Authorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		state = fmt.Sprintf("hcv-%d", time.Now().UnixNano())
	}
	authURL := svc.OAuth2.GetAuthorizationURL(state)
	writeJSON(w, http.StatusOK, map[string]string{
		"authorization_url": authURL,
		"state":             state,
	})
}

// handleOAuth2Callback — OAuth2 콜백 처리 (GET /api/v1/auth/oauth2/callback).
// 쿼리 파라미터: code (인증 코드). 토큰 교환 후 결과를 JSON으로 반환.
func (svc *Services) handleOAuth2Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "code parameter is required")
		return
	}

	token, err := svc.OAuth2.ExchangeCode(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, ErrCodeUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, token)
}

// ── Storage Snapshot Handlers ─────────────────────────

// handleStorageSnapshotRollback — 스토리지 스냅샷을 롤백한다 (POST /api/v1/storage/snapshots/{id}/rollback).
// 볼륨을 스냅샷 시점의 상태로 되돌린다.
func (svc *Services) handleStorageSnapshotRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Storage.RollbackSnapshot(id); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rolled back", "snapshot_id": id})
}

// handleStorageSnapshotClone — 스토리지 스냅샷에서 새 볼륨을 복제한다 (POST /api/v1/storage/snapshots/{id}/clone).
// 요청 본문: {"name": "새볼륨이름"}
// 성공 시 201 Created + 복제된 볼륨 정보를 반환한다.
func (svc *Services) handleStorageSnapshotClone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
		return
	}
	vol, err := svc.Storage.CloneSnapshot(id, req.Name)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, vol)
}

// handleDeleteStorageSnapshot — 스토리지 스냅샷을 삭제한다 (DELETE /api/v1/storage/snapshots/{id}).
// 성공 시 204 No Content를 반환한다.
func (svc *Services) handleDeleteStorageSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := svc.Storage.DeleteSnapshot(id); err != nil {
		writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
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

// handleCreateZone — 새 SDN 존을 생성한다 (POST /api/v1/network/zones).
// 요청 본문: {"name": "...", "type": "vxlan|vlan|simple", "bridge": "...", "mtu": N}
// 성공 시 201 Created + 생성된 존 정보를 반환한다.
func (svc *Services) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	if svc.Network == nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "network service not available")
		return
	}
	var req struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Bridge string `json:"bridge"`
		MTU    int    `json:"mtu"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "name is required")
		return
	}
	if req.Type == "" {
		req.Type = "simple"
	}
	zone := &network.Zone{
		Name:     req.Name,
		ZoneType: req.Type,
		Bridge:   req.Bridge,
		MTU:      req.MTU,
	}
	if err := svc.Network.CreateZone(zone); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, zone)
}

// handleDeleteZone — SDN 존을 삭제한다 (DELETE /api/v1/network/zones/{name}).
// 성공 시 204 No Content를 반환한다.
func (svc *Services) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	if svc.Network == nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "network service not available")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeBadRequest, "zone name is required")
		return
	}
	if err := svc.Network.DeleteZone(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// handleClusterLeader — 현재 클러스터 리더 정보를 반환한다 (GET /api/v1/cluster/leader).
//
// 응답: {"leader": "node-01", "is_self": true}
// HAServices.LeaderElection이 nil이면 단일 노드 모드로 동작하여
// 현재 노드 이름을 리더로 반환한다.
func (svc *Services) handleClusterLeader(w http.ResponseWriter, _ *http.Request) {
	leader := "unknown"
	isSelf := false

	if svc.HAServices != nil && svc.HAServices.LeaderElection != nil {
		le := svc.HAServices.LeaderElection
		if l, err := le.GetLeader(); err == nil {
			leader = l
		}
		isSelf = le.IsLeader()
	} else {
		// Single-node mode
		leader = ha.GetNodeName()
		isSelf = true
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"leader":  leader,
		"is_self": isSelf,
	})
}

// handleClusterPromote — 리더 재선출을 트리거한다 (POST /api/v1/cluster/promote).
//
// 현재 리더십을 사임(Resign)한 후 즉시 재선출(Campaign)을 시도한다.
// 다중 노드 환경에서 리더를 의도적으로 변경할 때 사용한다.
// 단일 노드 모드에서는 "single-node" 상태를 반환한다.
func (svc *Services) handleClusterPromote(w http.ResponseWriter, _ *http.Request) {
	if svc.HAServices == nil || svc.HAServices.LeaderElection == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "single-node",
			"message": "leader election not configured",
		})
		return
	}

	le := svc.HAServices.LeaderElection

	// Resign current leadership to trigger re-election
	if err := le.Resign(); err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal,
			fmt.Sprintf("resign failed: %v", err))
		return
	}

	// Re-campaign
	if err := le.Campaign(context.Background()); err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal,
			fmt.Sprintf("re-election failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "re-election triggered",
		"message": "leadership resigned, re-election in progress",
	})
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
