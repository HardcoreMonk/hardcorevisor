// Package main — HardCoreVisor Controller (Go)
//
// Orchestration layer managing VM lifecycle, storage, networking,
// and HA via REST API and gRPC services.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HardcoreMonk/hardcorevisor/controller/internal/api"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/compute"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/grpcapi"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/ha"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/network"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/peripheral"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/storage"
	"github.com/HardcoreMonk/hardcorevisor/controller/internal/store"
	"github.com/HardcoreMonk/hardcorevisor/controller/pkg/ffi"
)

const (
	defaultHTTPAddr = ":8080"
	defaultGRPCAddr = ":9090"
	version         = "0.1.0"
)

func main() {
	httpAddr := os.Getenv("HCV_API_ADDR")
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}
	grpcAddr := os.Getenv("HCV_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = defaultGRPCAddr
	}

	log.Printf("HardCoreVisor Controller v%s starting", version)

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
	etcdEndpoints := os.Getenv("HCV_ETCD_ENDPOINTS")
	kvStore := store.NewStore(etcdEndpoints)
	defer kvStore.Close()

	// Wrap compute service with persistence if using a real store
	var computeProvider compute.ComputeProvider = computeSvc
	if _, isMemory := kvStore.(*store.MemoryStore); !isMemory {
		persistent := compute.NewPersistentComputeService(computeSvc, kvStore)
		if err := persistent.LoadFromStore(); err != nil {
			log.Printf("WARNING: failed to load VMs from store: %v", err)
		}
		computeProvider = persistent
	}

	storageSvc := storage.NewService()
	networkSvc := network.NewService()
	peripheralSvc := peripheral.NewService()
	haSvc := ha.NewService()

	// ── REST API ──
	restServices := &api.Services{
		Compute:    computeProvider,
		Storage:    storageSvc,
		Network:    networkSvc,
		Peripheral: peripheralSvc,
		HA:         haSvc,
		Version: api.VersionInfo{
			Version:   version,
			GitCommit: "dev",
			BuildDate: time.Now().Format(time.RFC3339),
			VMCore:    core.Version(),
		},
	}
	router := api.NewRouter(restServices)

	httpSrv := &http.Server{
		Addr:         httpAddr,
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
		log.Printf("REST API listening on %s", httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("REST listen error: %v", err)
		}
	}()

	go func() {
		log.Printf("gRPC listening on %s", grpcAddr)
		if err := grpcapi.ListenAndServe(grpcSrv, grpcAddr); err != nil {
			log.Fatalf("gRPC listen error: %v", err)
		}
	}()

	log.Printf("Controller ready — REST %s, gRPC %s", httpAddr, grpcAddr)
	<-ctx.Done()

	log.Println("Shutting down gracefully...")
	grpcSrv.GracefulStop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	core.Shutdown()
	fmt.Println("Controller stopped.")
}
