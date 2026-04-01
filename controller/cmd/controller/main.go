// Package main — HardCoreVisor Controller (Go 오케스트레이션 레이어)
//
// VM 생명주기, 스토리지, 네트워크, HA를 REST API(:18080)와 gRPC(:19090)로
// 동시에 서비스하는 메인 프로세스이다.
//
// # 아키텍처 위치
//
//	┌──────────────────────────────────────┐
//	│  hcvtui (Rust TUI)   hcvctl (CLI)   │  ← 클라이언트
//	│        │ REST              │ REST    │
//	│        ▼                   ▼         │
//	│  ┌─── Controller (이 바이너리) ───┐  │
//	│  │ REST API (:18080) + gRPC (:19090)│  │
//	│  │ Services: Compute, Storage,    │  │
//	│  │   Network, Peripheral, HA      │  │
//	│  └───────────────────────────────-┘  │
//	│        │ FFI / QMP                   │
//	│        ▼                             │
//	│  vmcore (Rust) / QEMU                │  ← VMM 백엔드
//	└──────────────────────────────────────┘
//
// # 초기화 순서
//
//  1. 설정 파일 로드 (hcv.yaml + 환경변수 오버라이드)
//  2. 구조화 로깅 초기화 (slog)
//  3. 서비스 초기화 (Compute, Storage, Network, Peripheral, HA, Backup)
//  4. 상태 저장소 연결 (etcd 또는 인메모리 폴백)
//  5. REST 라우터 + 미들웨어 체인 구성
//  6. gRPC 서버 구성
//  7. 동시 서빙 시작 (goroutine)
//  8. SIGINT/SIGTERM 대기 → 그레이스풀 셧다운
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/api"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/auth"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/backup"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/config"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/grpcapi"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/image"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/logging"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/task"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/tracing"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

const version = "0.1.0"

func main() {
	// ── 설정 파일 로드 (hcv.yaml + 환경변수 오버라이드) ──
	cfg, err := config.Load("hcv.yaml")
	if err != nil {
		// Fall back to defaults if config load fails for non-file-not-found errors.
		fmt.Printf("WARNING: failed to load hcv.yaml: %v — using defaults\n", err)
		cfg = config.DefaultConfig()
	}

	// ── 설정 유효성 검증 ──
	if warnings := cfg.Validate(); len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("CONFIG WARNING: %s\n", w)
		}
	}

	// ── 구조화 로깅 초기화 (slog 기반, text/json 형식) ──
	logging.Setup(cfg.Log.Level, cfg.Log.Format)

	// ── OpenTelemetry 분산 트레이싱 초기화 (HCV_OTEL_ENDPOINT 설정 시 활성) ──
	tracingShutdown, err := tracing.Setup("hardcorevisor-controller", cfg.Otel.Endpoint)
	if err != nil {
		slog.Warn("tracing setup failed", "error", err)
	}
	defer tracingShutdown()

	// ── 시그널 컨텍스트 (셧다운 대기용) ──
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	nodeName := ha.GetNodeName()
	slog.Info("HardCoreVisor Controller starting", "version", version, "node", nodeName)

	// ── 서비스 초기화 ──
	// CGo 빌드 태그에 따라 실제(CGoVMCore) 또는 Mock(MockVMCore) 백엔드 자동 선택
	core := ffi.NewBackend()
	core.Init()

	// ── Triple VMM 백엔드 구성 (Phase 16 추가: LXC) ──
	// RustVMM: 고성능 Linux microVM (Handle 1~9999)
	// QEMU: 범용 VM — Windows, GPU 패스스루, 레거시 OS (Handle 10000~19999)
	// LXC: 경량 Linux 컨테이너 — 빠른 시작, 낮은 오버헤드 (Handle 20000+)
	rustVMM := compute.NewRustVMMBackend(core)
	// QEMU 모드: config에서 real/emulated 선택 (HCV_QEMU_MODE 환경변수)
	qemuEmulated := cfg.QEMU.Mode != "real"
	qemuSocketDir := cfg.QEMU.SocketDir
	if qemuSocketDir == "" {
		qemuSocketDir = "/tmp/hcv"
	}
	qemuBackend := compute.NewQEMUBackend(&compute.QEMUConfig{
		Emulated:  qemuEmulated,
		QMPSocket: qemuSocketDir,
	})
	if !qemuEmulated {
		slog.Info("QEMU Real mode enabled", "socket_dir", qemuSocketDir)
	}
	lxcBackend := compute.NewLXCBackend(&compute.LXCBackendConfig{Emulated: true})
	// BackendSelector: 워크로드 특성에 따라 적합한 VMM을 자동 선택
	// PolicyAuto: GPU/대형 → QEMU, 컨테이너 → LXC, 경량 Linux → RustVMM
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(rustVMM)
	selector.Register(qemuBackend)
	selector.Register(lxcBackend)
	computeSvc := compute.NewComputeService(selector, rustVMM)

	// ── 상태 저장소 (etcd 또는 인메모리 폴백) ──
	rawStore := store.NewStore(cfg.Etcd.Endpoints)
	defer rawStore.Close()
	// Circuit Breaker로 래핑 — etcd 장애 시 5회 실패 후 30초 차단
	var kvStore store.Store
	if _, isMemory := rawStore.(*store.MemoryStore); !isMemory {
		kvStore = store.NewResilientStore(rawStore)
		slog.Info("etcd store protected by circuit breaker")
	} else {
		kvStore = rawStore
	}

	// 실제 저장소(etcd) 사용 시 PersistentComputeService로 래핑하여 VM 상태 영속화
	var computeProvider compute.ComputeProvider = computeSvc
	if _, isMemory := kvStore.(*store.MemoryStore); !isMemory {
		persistent := compute.NewPersistentComputeService(computeSvc, kvStore)
		if err := persistent.LoadFromStore(); err != nil {
			slog.Warn("failed to load VMs from store", "error", err)
		}
		computeProvider = persistent
	}

	var storageSvc *storage.Service
	switch cfg.Storage.Driver {
	case "zfs":
		slog.Info("Using ZFS storage driver")
		storageSvc = storage.NewServiceWithDriver(&storage.ZFSDriver{})
	case "ceph":
		slog.Info("Using Ceph RBD storage driver", "pool", cfg.Storage.CephPool)
		storageSvc = storage.NewServiceWithDriver(storage.NewCephDriver(cfg.Storage.CephPool))
	default:
		storageSvc = storage.NewService()
	}
	imageSvc := image.NewService("/var/lib/hcv/images")
	networkSvc := network.NewService()
	var peripheralSvc *peripheral.Service
	switch cfg.Peripheral.Driver {
	case "sysfs":
		slog.Info("Using sysfs peripheral driver")
		peripheralSvc = peripheral.NewServiceWithDriver(peripheral.NewSysfsDriver())
	default:
		peripheralSvc = peripheral.NewService()
	}

	// HA service — use etcd driver when etcd is available
	var haSvc *ha.Service
	var haServices *api.HAServices
	etcdEndpoints := cfg.Etcd.GetEndpoints()
	if _, isMemory := kvStore.(*store.MemoryStore); !isMemory {
		slog.Info("Using etcd HA driver")
		etcdDriver := ha.NewEtcdDriver(kvStore, nodeName)
		haSvc = ha.NewServiceWithDriver(etcdDriver)
		hb := ha.NewHeartbeat(kvStore, nodeName, 10*time.Second)
		hb.Start()
		defer hb.Stop()
		defer hb.Deregister()

		// Initialize HA production components
		leaderElection, err := ha.NewLeaderElection(etcdEndpoints, nodeName, 15)
		if err != nil {
			slog.Warn("failed to create leader election", "error", err)
		} else {
			if err := leaderElection.Campaign(ctx); err != nil {
				slog.Warn("leader campaign failed", "error", err)
			}
			etcdDriver.SetLeaderElection(leaderElection)
			defer leaderElection.Close()
		}

		lockManager := ha.NewLockManager(etcdEndpoints)
		defer lockManager.Close()

		failoverManager := ha.NewFailoverManager(leaderElection, lockManager)
		failoverManager.SetDriver(etcdDriver)

		// Wire failure detector to failover manager
		fd := ha.NewFailureDetector(etcdDriver, 10*time.Second)
		fd.OnNodeDown(failoverManager.HandleNodeDown)
		fd.Start(ctx)
		defer fd.Stop()

		haServices = &api.HAServices{
			LeaderElection:  leaderElection,
			LockManager:     lockManager,
			FailoverManager: failoverManager,
		}
	} else {
		haSvc = ha.NewService()

		// Single-node mode HA components
		leaderElection, _ := ha.NewLeaderElection(nil, nodeName, 15)
		lockManager := ha.NewLockManager(nil)
		failoverManager := ha.NewFailoverManager(leaderElection, lockManager)

		haServices = &api.HAServices{
			LeaderElection:  leaderElection,
			LockManager:     lockManager,
			FailoverManager: failoverManager,
		}
	}
	backupSvc := backup.NewService(storageSvc)
	// 비동기 태스크 서비스 초기화 — 마이그레이션 등 장시간 작업의 진행 상태를 추적한다.
	// 인메모리 저장이므로 Controller 재시작 시 태스크 기록이 초기화된다.
	// Services.Task에 주입하여 API 핸들러(handleMigrateVM 등)에서 비동기 태스크 생성에 사용.
	taskSvc := task.NewTaskService()

	// ── JWT Auth + UserDB 초기화 (Phase 17) ──
	// UserDB: bbolt 기반 사용자 DB (bcrypt 해시 비밀번호 저장)
	// JWTService: HMAC-SHA256 JWT 토큰 발급/검증 (기본 TTL: 24시간)
	// SeedDefaultAdmin(): 최초 실행 시 admin/admin 기본 계정 생성
	var authServices *api.AuthServices
	dbPath := cfg.Auth.DBPath
	if dbPath == "" {
		dbPath = "hcv.db"
	}
	userDB, err := auth.NewUserDB(dbPath)
	if err != nil {
		slog.Error("failed to open user database", "error", err, "path", dbPath)
	} else {
		defer userDB.Close()
		userDB.SeedDefaultAdmin()
		if cfg.Auth.JWTSecret == "" {
			slog.Warn("HCV_JWT_SECRET not set — using random key (tokens invalidated on restart)")
		}
		jwtSvc := auth.NewJWTService(cfg.Auth.JWTSecret, 24*time.Hour)
		authServices = &api.AuthServices{
			UserDB:     userDB,
			JWTService: jwtSvc,
		}
		slog.Info("JWT auth initialized", "db", dbPath)
	}

	// ── REST API 라우터 구성 ──
	eventHub := api.NewEventHub()
	restServices := &api.Services{
		Compute:    computeProvider,
		Storage:    storageSvc,
		Network:    networkSvc,
		Peripheral: peripheralSvc,
		HA:         haSvc,
		HAServices: haServices,
		Backup:     backupSvc,
		Image:      imageSvc,
		LXC:        lxcBackend,
		Task:       taskSvc,
		Auth:       authServices,
		EventHub:   eventHub,
		Version: api.VersionInfo{
			Version:   version,
			GitCommit: "dev",
			BuildDate: time.Now().Format(time.RFC3339),
			VMCore:    core.Version(),
		},
	}
	// RBAC 사용자 로드 (설정 파일 + HCV_RBAC_USERS 환경변수 병합 완료 상태)
	rbacUsers := auth.ParseUsers(cfg.Auth.Users)
	// 미들웨어 체인: RequestID → Audit → Logging → Metrics → RBAC → CORS → RateLimit → Recovery
	router := api.NewRouter(restServices, rbacUsers)

	httpSrv := &http.Server{
		Addr:         cfg.API.Addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── gRPC ──
	grpcSvc := &grpcapi.Services{
		Compute:    computeProvider,
		Storage:    storageSvc,
		Peripheral: peripheralSvc,
	}
	grpcSrv := grpcapi.NewServer(grpcSvc)

	// ── 서버 시작 (REST + gRPC 동시 서빙) ──
	go func() {
		slog.Info("REST API listening", "addr", cfg.API.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("REST listen error", "error", err)
		}
	}()

	go func() {
		slog.Info("gRPC listening", "addr", cfg.GRPC.Addr)
		if err := grpcapi.ListenAndServe(grpcSrv, cfg.GRPC.Addr); err != nil {
			slog.Error("gRPC listen error", "error", err)
		}
	}()

	slog.Info("Controller ready", "rest", cfg.API.Addr, "grpc", cfg.GRPC.Addr)
	<-ctx.Done()

	slog.Info("Shutting down gracefully...")
	grpcSrv.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	core.Shutdown()
	fmt.Println("Controller stopped.")
}
