# HardCoreVisor

> **"타협 없는 성능·보안·안정성의 하이퍼바이저 감독자"**

VMware × Proxmox 두 플랫폼의 모든 강점을 통합한 차세대 하이브리드 가상화 플랫폼.
QEMU(범용) + rust-vmm(고성능) Dual VMM 아키텍처로 워크로드에 최적화된 가상화를 제공한다.

## Architecture

```
┌──────────────────────────────────────────────────────┐
│  hcvtui (Rust/Ratatui)  │  Web UI (React)           │  ← Management Layer
├──────────────────────────────────────────────────────┤
│  hcvctl (Go/Cobra)      │  REST + gRPC API          │  ← API Layer
├──────────────────────────────────────────────────────┤
│  Go Controller           REST :8080 / gRPC :9090     │
│  ┌──────────────────────────────────────────────────┐│
│  │            Backend Selector (Auto/Manual)        ││  ← Dual VMM
│  │     ┌──────────────┬──────────────────┐          ││
│  │     │ QEMU Backend │ rust-vmm Backend │          ││
│  └─────┴──────────────┴──────────────────┘──────────┘│
│  ┌─────────┬─────────┬──────────┬────────┬────────┐ │
│  │Compute  │Storage  │Peripheral│Network │HA Mgr  │ │  ← Orchestration
│  │Service  │Agent    │Manager   │Control │        │ │
│  └────┬────┴────┬────┴──────────┴────────┴────────┘ │
│       │ CGo FFI │ gRPC (17 RPCs)                     │
├───────┴─────────┴────────────────────────────────────┤
│  vmcore (Rust staticlib) — 55 FFI functions          │
│  ┌─────────┬──────────┬───────────┬───────────────┐ │
│  │kvm_mgr  │vcpu_mgr  │memory_mgr │event_ring     │ │  ← KVM Core
│  │(State   │(Typestate)│(repr(C))  │(Lock-Free     │ │
│  │ Machine)│          │           │ SPSC)         │ │
│  ├─────────┼──────────┴───────────┴───────────────┤ │
│  │kvm_sys  │ virtio_split_queue │ virtio_blk      │ │  ← Device Emu
│  │(/dev/kvm│ io_engine (io_uring async I/O)       │ │
│  │ ioctl)  │                    │                 │ │
│  ├─────────┴────────────────────┴─────────────────┤ │
│  │ panic_barrier (catch_unwind FFI safety)        │ │
│  └────────────────────────────────────────────────┘ │
├──────────────────────────────────────────────────────┤
│  Linux KVM / QEMU                                    │  ← Hypervisor
└──────────────────────────────────────────────────────┘
```

## Quick Start

```bash
# Prerequisites: Rust 1.82+, Go 1.24+, just, /dev/kvm (optional)
git clone https://github.com/HardcoreMonk/hardcorevisor.git
cd hardcorevisor

# Build everything
just build

# Run tests (Rust 65 + Go 11 = 76 tests)
just test

# Start Go Controller + TUI (2 terminals)
just go-run          # Terminal 1: REST :8080 + gRPC :9090
just tui             # Terminal 2: Live dashboard

# Start dev services (etcd, Prometheus, Grafana)
just dev-up
```

## Project Structure

| Directory | Language | Purpose |
|-----------|----------|---------|
| `vmcore/` | Rust | KVM core staticlib — 55 FFI, kvm_sys/kvm_boot, io_uring engine, virtio-blk+io pipeline, panic barrier |
| `hcvtui/` | Rust | Ratatui TUI — live dashboard, VM manager with API polling |
| `controller/` | Go | Orchestration — Compute (RustVMM+QEMU Dual VMM), Storage, Network, Peripheral, HA |
| `proto/` | Protobuf | gRPC service definitions → `controller/pkg/proto/` (17 RPCs) |
| `deploy/` | Docker | Dev stack (etcd, Prometheus, Grafana, Controller) |
| `scripts/` | Shell | Dev setup, E2E test runner, proto generation |

## Development

```bash
just --list          # Show all available commands
just check           # Run lint + test (pre-commit)
just quick           # Quick pre-commit check (<30s)
just e2e             # Full E2E integration tests
just proto-gen       # Regenerate Go code from proto files
just go-run          # Controller: REST :8080 + gRPC :9090
just rust-watch-tui  # Watch & run TUI on file change
just audit           # Security audit (cargo-audit + govulncheck)
```

## Testing

```bash
just check                    # 전체 lint + test (권장)
just quick                    # 빠른 프리커밋 (<30초, 8단계)

# Rust (65 tests)
just rust-test-vmcore         # vmcore 전체
just rust-test-kvm            # 실제 /dev/kvm ioctl (KVM 필요)
just rust-test-mod kvm_mgr    # 특정 모듈

# Go (11 tests — rustvmm + qemu dual backend)
just go-test                  # 전체
just go-test-e2e              # E2E만 (8개)
just go-test-api              # API 유닛만 (3개)

# REST + gRPC 수동 (Controller 실행 후)
just go-run                   # REST :8080 + gRPC :9090
curl -s localhost:8080/api/v1/vms | jq
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"test","vcpus":2,"memory_mb":4096}' | jq
grpcurl -plaintext localhost:9090 hardcorevisor.compute.v1.ComputeService/ListVMs

# TUI 라이브 (Controller 실행 후)
just tui                      # 1-6: 화면 전환, s/x/p/d: VM 제어
```

## API

### REST (28 endpoints, :8080)

| Service | Endpoints | Examples |
|---------|-----------|---------|
| **Compute** | 10 | `GET /api/v1/vms`, `POST /api/v1/vms/{id}/start` |
| **Storage** | 4 | `GET /api/v1/storage/pools`, `POST /api/v1/storage/volumes` |
| **Network** | 3 | `GET /api/v1/network/zones`, `GET /api/v1/network/firewall` |
| **Peripheral** | 3 | `GET /api/v1/devices`, `POST /api/v1/devices/{id}/attach` |
| **HA/Cluster** | 4 | `GET /api/v1/cluster/status`, `POST /api/v1/cluster/fence/{node}` |

### gRPC (17 RPCs, :9090)

| Service | RPCs | Proto Package |
|---------|------|---------------|
| **ComputeService** | 9 | `hardcorevisor.compute.v1` |
| **StorageAgent** | 5 | `hardcorevisor.storage.v1` |
| **PeripheralManager** | 3 | `hardcorevisor.peripheral.v1` |

gRPC reflection 활성 — `grpcurl -plaintext localhost:9090 list`

## License

AGPL-3.0
