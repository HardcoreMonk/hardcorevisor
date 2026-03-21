# HardCoreVisor Integration Tests

## Overview

This directory contains end-to-end integration tests that verify the full
HardCoreVisor stack: vmcore (Rust) → Go Controller (REST API) → hcvctl (CLI).

## Test Structure

```
tests/
  e2e_vm_lifecycle_test.go    # Full VM CRUD + state transitions + concurrency
deploy/
  docker-compose.yml          # etcd + Prometheus + Grafana + Controller
  Dockerfile.controller       # Multi-stage Go Controller build
  prometheus.yml              # Prometheus scrape config
scripts/
  run-e2e.sh                  # Full E2E test runner (4 phases)
  quick-check.sh              # Fast pre-commit check (<30s)
.github/workflows/
  ci.yml                      # GitHub Actions CI pipeline
```

## Running Tests

### Quick Check (pre-commit, <30s)
```bash
just quick
# or: ./scripts/quick-check.sh
```

### Full E2E (all phases)
```bash
just e2e
# or: ./scripts/run-e2e.sh
```

Phases:
1. **Rust vmcore**: build → test → clippy → audit → verify vmcore.h + libvmcore.a
2. **Go Controller**: build → test (race) → vet → lint → vulncheck
3. **E2E Integration**: Full VM lifecycle through REST API with Mock FFI
4. **Docker Stack** (optional): build → up → healthcheck → live E2E

### E2E with Docker Stack
```bash
just e2e-stack
# or: ./scripts/run-e2e.sh --with-stack
```

### Individual Phases
```bash
just e2e-rust    # Rust only
just e2e-go      # Go only
```

## E2E Test Cases (7 tests, ~30 assertions)

| Test | Assertions | Description |
|------|-----------|-------------|
| `TestE2E_FullVMLifecycle` | 14 | Create → List → Get → Start → Pause → Resume → Stop → Delete → Verify 404 |
| `TestE2E_InvalidStateTransitions` | 3 | configured→pause (409), configured→resume (409), running→start (409) |
| `TestE2E_BackendSelection` | 2 | List backends, invalid backend error |
| `TestE2E_ConcurrentVMCreation` | 10 | 10 goroutines creating VMs simultaneously (race safety) |
| `TestE2E_StubEndpoints` | 4 | /nodes, /storage/pools, /network/zones, /cluster/status |
| `TestE2E_MiddlewareChain` | 2 | X-Request-Id header, CORS preflight |

## Docker Stack

```bash
just stack-up     # Start: etcd + Prometheus + Grafana + Controller
just stack-down   # Stop all
just stack-logs   # Follow logs
```

Services:
- **Controller**: http://localhost:8080 (REST API)
- **Prometheus**: http://localhost:9090
- **Grafana**: http://localhost:3000 (admin/admin)
- **etcd**: http://localhost:2379

## CI Pipeline (GitHub Actions)

```
rust-lint ──→ rust-test ──┐
                          ├──→ e2e ──→ build
go-lint ────→ go-test ────┘
rust-security (parallel)
go-security (parallel)
```

Jobs: 7 total (lint → test → e2e → build → security × 2)
