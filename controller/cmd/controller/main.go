// Package main — HardCoreVisor Controller (Go 오케스트레이션 레이어)
//
// VM 생명주기, 스토리지, 네트워크, HA를 REST API(:8080)와 gRPC(:9090)로
// 동시에 서비스하는 메인 프로세스이다.
//
// # 아키텍처 위치
//
//	┌──────────────────────────────────────┐
//	│  hcvtui (Rust TUI)   hcvctl (CLI)   │  ← 클라이언트
//	│        │ REST              │ REST    │
//	│        ▼                   ▼         │
//	│  ┌─── Controller (이 바이너리) ───┐  │
//	│  │ REST API (:8080) + gRPC (:9090)│  │
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

	// ── 구조화 로깅 초기화 (slog 기반, text/json 형식) ──
	logging.Setup(cfg.Log.Level, cfg.Log.Format)

	nodeName := ha.GetNodeName()
	slog.Info("HardCoreVisor Controller starting", "version", version, "node", nodeName)

	// ── 서비스 초기화 ──
	// MockVMCore: 실제 libvmcore.a 없이 순수 Go로 VM 상태를 관리하는 테스트 백엔드
	core := ffi.NewMockVMCore()
	core.Init()

	// Dual VMM 백엔드 구성: RustVMM(경량 microVM) + QEMU(범용 VM)
	rustVMM := compute.NewRustVMMBackend(core)
	qemuBackend := compute.NewQEMUBackend(&compute.QEMUConfig{Emulated: true})
	// BackendSelector: 워크로드 특성에 따라 적합한 VMM을 자동 선택
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(rustVMM)
	selector.Register(qemuBackend)
	computeSvc := compute.NewComputeService(selector, rustVMM)

	// ── 상태 저장소 (etcd 또는 인메모리 폴백) ──
	kvStore := store.NewStore(cfg.Etcd.Endpoints)
	defer kvStore.Close()

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
	if _, isMemory := kvStore.(*store.MemoryStore); !isMemory {
		slog.Info("Using etcd HA driver")
		haSvc = ha.NewServiceWithDriver(ha.NewEtcdDriver(kvStore, nodeName))
		hb := ha.NewHeartbeat(kvStore, nodeName, 10*time.Second)
		hb.Start()
		defer hb.Stop()
	} else {
		haSvc = ha.NewService()
	}
	backupSvc := backup.NewService(storageSvc)

	// ── REST API 라우터 구성 ──
	eventHub := api.NewEventHub()
	restServices := &api.Services{
		Compute:    computeProvider,
		Storage:    storageSvc,
		Network:    networkSvc,
		Peripheral: peripheralSvc,
		HA:         haSvc,
		Backup:     backupSvc,
		Image:      imageSvc,
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
