// Package main — HardCoreVisor Controller (Go)
//
// Orchestration layer managing VM lifecycle, storage, networking,
// and HA via REST API and gRPC services.
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
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/logging"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

const version = "0.1.0"

func main() {
	// ── Load configuration (hcv.yaml + env var overlay) ──
	cfg, err := config.Load("hcv.yaml")
	if err != nil {
		// Fall back to defaults if config load fails for non-file-not-found errors.
		fmt.Printf("WARNING: failed to load hcv.yaml: %v — using defaults\n", err)
		cfg = config.DefaultConfig()
	}

	// ── Structured logging ──
	logging.Setup(cfg.Log.Level, cfg.Log.Format)

	slog.Info("HardCoreVisor Controller starting", "version", version)

	// ── Initialize services ──
	core := ffi.NewMockVMCore()
	core.Init()

	rustVMM := compute.NewRustVMMBackend(core)
	qemuBackend := compute.NewQEMUBackend(&compute.QEMUConfig{Emulated: true})
	selector := compute.NewBackendSelector(compute.PolicyAuto)
	selector.Register(rustVMM)
	selector.Register(qemuBackend)
	computeSvc := compute.NewComputeService(selector, rustVMM)

	// ── State Store (etcd or in-memory) ──
	kvStore := store.NewStore(cfg.Etcd.Endpoints)
	defer kvStore.Close()

	// Wrap compute service with persistence if using a real store
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
	default:
		storageSvc = storage.NewService()
	}
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
		haSvc = ha.NewServiceWithDriver(ha.NewEtcdDriver(kvStore, "node-01"))
	} else {
		haSvc = ha.NewService()
	}
	backupSvc := backup.NewService(storageSvc)

	// ── REST API ──
	restServices := &api.Services{
		Compute:    computeProvider,
		Storage:    storageSvc,
		Network:    networkSvc,
		Peripheral: peripheralSvc,
		HA:         haSvc,
		Backup:     backupSvc,
		Version: api.VersionInfo{
			Version:   version,
			GitCommit: "dev",
			BuildDate: time.Now().Format(time.RFC3339),
			VMCore:    core.Version(),
		},
	}
	// Load RBAC users from config (config already merged env vars)
	rbacUsers := auth.ParseUsers(cfg.Auth.Users)
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

	// ── Start servers ──
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
