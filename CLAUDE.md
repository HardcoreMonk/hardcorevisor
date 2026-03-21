# CLAUDE.md

이 파일은 Claude Code (claude.ai/code)가 본 저장소에서 코드 작업을 수행할 때 참고하는 가이드입니다.

## 프로젝트 개요

HardCoreVisor는 Rust(KVM 코어)와 Go(오케스트레이션 컨트롤러)를 결합한 하이브리드 가상화 플랫폼이다. Dual VMM 아키텍처를 채택하여 QEMU(범용 VM: Windows, GPU 패스스루, 레거시)와 rust-vmm(고성능 Linux microVM)을 동시 운영한다. Go Controller가 워크로드 특성에 따라 Backend Selector 패턴으로 적합한 VMM을 자동 선택한다.

## 저장소 구조

메인 프로젝트는 `hardcorevisor/` 디렉터리에 위치한다. (Rust 워크스페이스 루트이자 Go 모듈 루트)

```
hardcorevisor/
├── vmcore/                    # Rust staticlib (libvmcore.a) — KVM 코어
│   ├── src/
│   │   ├── lib.rs             # FFI 진입점 (hcv_init, hcv_version, hcv_shutdown)
│   │   ├── panic_barrier.rs   # catch_unwind FFI 안전성 래퍼 + ErrorCode 정의
│   │   ├── kvm_mgr.rs         # VM 레지스트리, 상태 머신 (VmState), FFI 9개 함수
│   │   ├── kvm_sys.rs         # 실제 /dev/kvm ioctl 래퍼 (KvmSystem, KvmVm, KvmVcpu)
│   │   ├── kvm_boot.rs        # x86 미니 게스트 부팅 (KVM_RUN 루프, I/O exit) + FFI 1개
│   │   ├── vcpu_mgr.rs        # Typestate vCPU + FFI 10개 함수 + 레지스터 관리
│   │   ├── memory_mgr.rs      # 게스트 메모리 영역 + dirty log + FFI 8개 함수
│   │   ├── event_ring.rs      # Lock-free SPSC 이벤트 버스 + FFI 6개 함수
│   │   ├── io_engine.rs       # io_uring 비동기 디스크 I/O 엔진 + FFI 8개 함수
│   │   ├── virtio_split_queue.rs  # Virtio 1.x Split Virtqueue (내부 모듈)
│   │   ├── virtio_blk.rs      # Virtio 블록 디바이스 에뮬레이션 + FFI 7개 함수
│   │   ├── virtio_blk_io.rs   # virtio-blk + io_uring 파이프라인 + FFI 3개 함수
│   │   └── virtio_net.rs      # Virtio 네트워크 디바이스 에뮬레이션 + FFI 8개 함수
│   ├── build.rs               # cbindgen으로 vmcore.h 자동 생성
│   ├── cbindgen.toml          # C 헤더 생성 설정
│   └── Cargo.toml
├── hcvtui/                    # Rust Ratatui 터미널 UI (라이브 API 연동)
│   └── src/
│       ├── main.rs            # 엔트리포인트 (tokio async)
│       ├── app.rs             # App 상태, tick() 폴링, VM 액션, CreateFormState
│       ├── api_client.rs      # REST API 클라이언트 (reqwest, 3초 타임아웃)
│       ├── keybindings.rs     # vim 스타일 키바인딩 + VM 액션 (s/x/p/d/c)
│       └── ui/                # 6개 화면 (dashboard, vm_manager, storage, network, log, ha)
├── controller/                # Go 오케스트레이션 레이어
│   ├── cmd/
│   │   ├── controller/main.go # REST(:8080) + gRPC(:9090) 동시 서빙, 풀 서비스 모드
│   │   └── hcvctl/main.go     # CLI (Cobra, --output json/yaml/table, --tls, --user)
│   ├── internal/
│   │   ├── api/
│   │   │   ├── router.go      # Services 기반 REST 라우터 + 미들웨어 체인
│   │   │   └── router_test.go # 유닛 테스트 (Health, ListVMs, Version)
│   │   ├── grpcapi/           # gRPC 서비스 구현체
│   │   │   ├── server.go          # 통합 gRPC 서버 생성 + reflection
│   │   │   ├── compute_server.go  # ComputeService 9 RPC
│   │   │   ├── storage_server.go  # StorageAgent 5 RPC
│   │   │   └── peripheral_server.go  # PeripheralManager 3 RPC
│   │   ├── compute/
│   │   │   ├── compute.go        # ComputeService, RustVMMBackend, BackendSelector, ComputeProvider
│   │   │   ├── qemu_backend.go   # QEMUBackend — QMP 기반 범용 VM (Emulated/Real 모드)
│   │   │   └── persistent.go     # PersistentComputeService — etcd 상태 영속화 래퍼
│   │   ├── storage/storage.go # Storage Agent — 풀/볼륨/스냅샷 관리 (ZFS/Ceph)
│   │   ├── network/network.go # SDN Controller — 존/VNet/방화벽 관리 (VXLAN/nftables)
│   │   ├── peripheral/peripheral.go  # Peripheral Manager — GPU/NIC/USB 패스스루
│   │   ├── ha/ha.go           # HA Manager — 클러스터 상태/quorum/펜싱
│   │   ├── store/etcd.go      # etcd/메모리 상태 저장소 (Store 인터페이스)
│   │   ├── metrics/           # Prometheus 메트릭 (metrics.go + collector.go)
│   │   ├── auth/              # RBAC (rbac.go) + 감사 로깅 (audit.go)
│   │   └── backup/backup.go   # VM 백업 서비스 (스냅샷 기반)
│   ├── pkg/
│   │   ├── ffi/
│   │   │   ├── errors.go      # FFIError 타입 + 에러 코드 상수 (공통)
│   │   │   ├── mock.go        # MockVMCore — 순수 Go 테스트 백엔드
│   │   │   └── vmcore.go      # CGo 바인딩 (빌드 태그: cgo_vmcore)
│   │   └── proto/             # protoc 생성 Go 코드
│   │       ├── computepb/     # ComputeService (9 RPC)
│   │       ├── storagepb/     # StorageAgent (5 RPC)
│   │       └── peripheralpb/  # PeripheralManager (3 RPC)
│   ├── tests/
│   │   └── e2e_vm_lifecycle_test.go  # E2E 통합 테스트 8개
│   └── go.mod
├── proto/                     # gRPC 서비스 정의 원본 (compute, storage, peripheral)
├── deploy/
│   ├── docker-compose.yml     # etcd + Prometheus + Grafana + Controller
│   ├── Dockerfile.controller  # 멀티스테이지 Go 빌드 (distroless)
│   ├── prometheus.yml         # Prometheus 수집 설정 + rule_files
│   ├── alert-rules.yml        # Prometheus 알람 규칙 (NodeDown, StorageHigh 등)
│   └── grafana/               # Grafana 프로비저닝
│       ├── provisioning/dashboards/dashboard.yml
│       ├── provisioning/datasources/datasource.yml
│       └── dashboards/hardcorevisor.json  # 5패널 대시보드
├── scripts/
│   ├── setup-dev.sh           # 개발 환경 검증
│   ├── proto-gen.sh           # protoc Go 코드 생성
│   ├── fix-build.sh           # 빌드 수정 스크립트
│   ├── run-e2e.sh             # E2E 통합 테스트 러너
│   ├── quick-check.sh         # 빠른 프리커밋 (<30초)
│   └── stack-smoke-test.sh    # Docker 스택 스모크 테스트 (17개)
├── Cargo.toml                 # Rust 워크스페이스 (vmcore, hcvtui)
├── justfile                   # 통합 빌드 시스템
└── README.md
```

## 빌드 및 테스트 명령어

모든 명령어는 `hardcorevisor/` 디렉터리에서 `just`(커맨드 러너)로 실행한다:

```bash
just build            # 전체 빌드 (Rust 워크스페이스 + Go)
just test             # 전체 테스트 (Rust 70 + Go 11 = 81)
just check            # 프리커밋 전체 검증: lint + test
just lint             # Rust clippy + fmt + Go vet
just quick            # 빠른 프리커밋 (<30초, 8단계)

# Rust 전용
just rust-test            # 전체 Rust 테스트 (직렬 실행)
just rust-test-vmcore     # vmcore 테스트만 (70개)
just rust-test-kvm        # 실제 /dev/kvm ioctl 테스트 (2개, KVM 필요)
just rust-test-mod kvm_mgr  # 특정 모듈 테스트 (kvm_mgr, vcpu_mgr, virtio_blk 등)
just rust-clippy          # cargo clippy --workspace -- -D warnings
just rust-fmt             # 포맷 검사 (--check)
just rust-fmt-fix         # 포맷 자동 수정
just rust-watch-vmcore    # vmcore 테스트 변경 감지 자동 실행
just tui                  # TUI 실행: cargo run -p hcvtui

# Go 전용
just go-test          # 전체 Go 테스트 (race detector, 11개)
just go-test-e2e      # E2E 통합 테스트만 (8개)
just go-test-api      # API 유닛 테스트만 (3개)
just go-vet           # go vet ./...
just go-lint          # golangci-lint run --fast
just go-run           # 컨트롤러 실행 (REST :8080 + gRPC :9090, 풀 서비스 모드)
just go-hcvctl        # 버전 정보 주입하여 CLI 바이너리 빌드

# E2E 통합 테스트
just e2e              # 전체 E2E 스위트 (스크립트)
just e2e-rust         # Rust vmcore만
just e2e-go           # Go 컨트롤러만
just e2e-stack        # Docker 서비스 포함 E2E

# Proto / Docker / 보안
just proto-gen        # proto/*.proto에서 Go 코드 생성 → controller/pkg/proto/
just dev-up           # etcd + Prometheus + Grafana 시작
just dev-down         # 개발 서비스 중지
just docker-build     # Docker 이미지 빌드
just audit            # cargo audit + govulncheck
```

## 테스트 가이드

### 전체 검증

```bash
just check            # lint + test 한 번에 (가장 권장)
just quick            # 빠른 프리커밋 (<30초, 8단계 — fmt/clippy/test/build/vet/e2e)
```

### Rust vmcore 테스트 (70개)

```bash
just rust-test-vmcore                     # vmcore 전체 (70개, 직렬)
just rust-test-kvm                        # 실제 /dev/kvm ioctl (2개, --nocapture)
just rust-test-mod io_engine             # io_uring 비동기 I/O (6개)
just rust-test-mod kvm_mgr               # VM 상태 머신 (6개)
just rust-test-mod vcpu_mgr              # Typestate vCPU (5개)
just rust-test-mod memory_mgr            # 메모리 영역 (5개)
just rust-test-mod event_ring            # SPSC 이벤트 링 (7개)
just rust-test-mod panic_barrier         # 패닉 배리어 (7개)
just rust-test-mod virtio_split_queue    # Split Virtqueue (5개)
just rust-test-mod virtio_blk            # 블록 디바이스 (5개)
just rust-test-mod virtio_blk_io         # virtio-blk + io_uring 파이프라인 (8개)
just rust-test-mod kvm_boot              # KVM 미니 게스트 부팅 (3개, KVM 필요)
just rust-test-mod virtio_net            # Virtio 네트워크 디바이스 (5개)

# 특정 테스트 하나만
cargo test -p vmcore test_full_lifecycle -- --test-threads=1
```

### Go Controller 테스트 (11개)

```bash
just go-test              # 전체 (race detector, 11개)
just go-test-api          # API 유닛 테스트만 (3개)
just go-test-e2e          # E2E 통합 테스트만 (8개)

# E2E 개별 실행
cd controller
go test -v -race -run TestE2E_FullVMLifecycle ./tests/
go test -v -race -run TestE2E_InvalidStateTransitions ./tests/
go test -v -race -run TestE2E_BackendSelection ./tests/
go test -v -race -run TestE2E_QEMUBackendLifecycle ./tests/
go test -v -race -run TestE2E_MixedBackends ./tests/
go test -v -race -run TestE2E_ConcurrentVMCreation ./tests/
go test -v -race -run TestE2E_StubEndpoints ./tests/
go test -v -race -run TestE2E_MiddlewareChain ./tests/
```

### REST API 수동 테스트 (curl)

Controller를 시작한 뒤 별도 터미널에서 실행한다:

```bash
# 터미널 1: Controller 시작
just go-run
```

**Compute (VM 관리):**
```bash
curl -s localhost:8080/healthz | jq
curl -s localhost:8080/api/v1/version | jq
curl -s localhost:8080/api/v1/backends | jq

# VM 생명주기: 생성 → 시작 → 일시정지 → 재개 → 중지 → 삭제
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-vm","vcpus":2,"memory_mb":4096}' | jq
curl -s localhost:8080/api/v1/vms | jq
curl -s localhost:8080/api/v1/vms/1 | jq
curl -s -X POST localhost:8080/api/v1/vms/1/start | jq
curl -s -X POST localhost:8080/api/v1/vms/1/pause | jq
curl -s -X POST localhost:8080/api/v1/vms/1/resume | jq
curl -s -X POST localhost:8080/api/v1/vms/1/stop | jq
curl -s -X DELETE localhost:8080/api/v1/vms/1 -w '%{http_code}\n'

# QEMU 백엔드로 VM 생성 (Windows, GPU 패스스루 용도)
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"win-server","vcpus":8,"memory_mb":32768,"backend":"qemu"}' | jq

# 잘못된 상태 전이 (409 확인)
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"err-test","vcpus":1,"memory_mb":256}' | jq
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/api/v1/vms/2/pause
```

**Storage (스토리지):**
```bash
curl -s localhost:8080/api/v1/storage/pools | jq
curl -s localhost:8080/api/v1/storage/volumes | jq
curl -s -X POST localhost:8080/api/v1/storage/volumes \
  -H 'Content-Type: application/json' \
  -d '{"pool":"local-zfs","name":"disk-01","size_bytes":10737418240,"format":"qcow2"}' | jq
curl -s localhost:8080/api/v1/storage/volumes?pool=local-zfs | jq
curl -s -X DELETE localhost:8080/api/v1/storage/volumes/vol-1 -w '%{http_code}\n'
```

**Network (SDN):**
```bash
curl -s localhost:8080/api/v1/network/zones | jq
curl -s localhost:8080/api/v1/network/vnets | jq
curl -s localhost:8080/api/v1/network/vnets?zone=vxlan-zone | jq
curl -s localhost:8080/api/v1/network/firewall | jq
```

**Peripheral (디바이스 패스스루):**
```bash
curl -s localhost:8080/api/v1/devices | jq
curl -s 'localhost:8080/api/v1/devices?type=gpu' | jq
curl -s -X POST localhost:8080/api/v1/devices/gpu-0/attach \
  -H 'Content-Type: application/json' \
  -d '{"vm_handle":1}' | jq
curl -s -X POST localhost:8080/api/v1/devices/gpu-0/detach | jq
```

**HA / Cluster:**
```bash
curl -s localhost:8080/api/v1/cluster/status | jq
curl -s localhost:8080/api/v1/cluster/nodes | jq
curl -s localhost:8080/api/v1/nodes | jq
curl -s -X POST localhost:8080/api/v1/cluster/fence/node-03 \
  -H 'Content-Type: application/json' \
  -d '{"reason":"unresponsive","action":"reboot"}' | jq
```

### TUI 라이브 테스트

```bash
# 터미널 1: Controller (REST :8080 + gRPC :9090)
just go-run

# 터미널 2: TUI
just tui
# 또는 Controller 주소 지정:
HCV_API_ADDR=192.168.1.100:8080 cargo run -p hcvtui
```

### gRPC 수동 테스트 (grpcurl)

Controller 실행 후:

```bash
# 서비스 목록 (reflection 활성)
grpcurl -plaintext localhost:9090 list

# Compute
grpcurl -plaintext localhost:9090 hardcorevisor.compute.v1.ComputeService/ListVMs
grpcurl -plaintext -d '{"name":"grpc-vm","vcpus":2,"memory_mb":4096}' \
  localhost:9090 hardcorevisor.compute.v1.ComputeService/CreateVM

# Storage
grpcurl -plaintext localhost:9090 hardcorevisor.storage.v1.StorageAgent/ListPools

# Peripheral
grpcurl -plaintext localhost:9090 hardcorevisor.peripheral.v1.PeripheralManager/ListDevices
```

### hcvctl CLI 테스트

```bash
# 기본 (table 포맷)
hcvctl vm list
hcvctl storage pool list
hcvctl cluster status

# JSON 출력 (스크립팅용)
hcvctl vm list -o json | jq '.[].name'
hcvctl storage pool list -o yaml
hcvctl cluster status -o json

# TLS + 인증
hcvctl --tls-skip-verify --user admin --password secret vm list

# 쉘 자동 완성 설치
source <(hcvctl completion bash)   # bash
source <(hcvctl completion zsh)    # zsh
hcvctl completion fish | source    # fish

# Backup
hcvctl backup list
hcvctl backup create --vm-id 1 --vm-name web-01 --pool local-zfs
hcvctl backup delete --id backup-1
```

TUI 키바인딩:

| 키 | 동작 | 화면 |
|----|------|------|
| `1`-`6` | 화면 전환 (Dashboard/VMs/Storage/Network/Logs/HA) | 전체 |
| `j`/`k` 또는 화살표 | 목록 스크롤 | 전체 |
| `r` | 수동 새로고침 | 전체 |
| `s` | VM 시작 | VM Manager |
| `x` | VM 중지 | VM Manager |
| `p` | VM 일시정지 | VM Manager |
| `d` | VM 삭제 | VM Manager |
| `Enter` | VM 상세 뷰 팝업 | VM Manager |
| `c` | VM 생성 폼 열기 | VM Manager |
| `q` | 종료 | 전체 |

### 실제 KVM 테스트

```bash
# /dev/kvm 권한 확인
ls -la /dev/kvm

# KVM 테스트 실행 (VM 생성 + 메모리 매핑 + vCPU 생성)
cargo test -p vmcore kvm_sys -- --test-threads=1 --nocapture

# KVM 없는 환경에서는 자동 SKIP됨 (에러 아님)
```

### Lint / 포맷 / 보안

```bash
just lint             # 전체 (Rust clippy + fmt + Go vet)
just rust-fmt         # Rust 포맷 검사
just rust-fmt-fix     # Rust 포맷 자동 수정
just rust-clippy      # Rust clippy -D warnings
just go-vet           # Go vet
just go-lint          # golangci-lint (설치 필요)
just audit            # cargo audit + govulncheck
```

## 테스트 구조 요약

| 범위 | 위치 | 실행 방법 | 테스트 수 |
|------|------|----------|----------|
| vmcore 유닛 | `vmcore/src/*.rs` | `cargo test -p vmcore -- --test-threads=1` | 70 |
| KVM ioctl | `vmcore/src/kvm_sys.rs` | `cargo test -p vmcore kvm_sys` | 2 (포함) |
| API 유닛 | `controller/internal/api/router_test.go` | `go test ./internal/api/` | 3 |
| E2E 통합 | `controller/tests/e2e_vm_lifecycle_test.go` | `go test -race ./tests/` | 8 |
| **합계** | | `just test` | **81** |

E2E 테스트 스택: `MockVMCore` → `RustVMMBackend` → `BackendSelector` → `ComputeService` + `Storage/Network/Peripheral/HA` → `api.NewRouter(svc)` → `httptest.Server`

E2E 시나리오: 전체 VM 생명주기, 잘못된 상태 전이(409), 백엔드 선택(rustvmm+qemu), QEMU 백엔드 전체 생명주기, 혼합 백엔드(rustvmm+qemu 동시 운영), 동시성(10개 병렬 생성), 스텁 엔드포인트(storage/network/cluster), 미들웨어 체인(RequestID/CORS).

## 아키텍처: 핵심 설계 패턴

### FFI 경계 (Rust → Go)

핵심 통합 패턴: `vmcore`가 C staticlib으로 컴파일되어 CGo를 통해 Go에 링크된다. 총 63개 FFI 함수.

- **모든 `extern "C"` 함수는 반드시 `panic_barrier::catch()`로 래핑해야 한다.** Rust 패닉이 Go 런타임으로 전파되는 것을 방지하기 위한 필수 규칙이다.
- **raw pointer를 역참조하는 FFI 함수에는 `#[allow(clippy::not_unsafe_ptr_arg_deref)]`를 붙인다.** `extern "C"` 함수는 Rust의 `unsafe fn` 마킹 대신 panic_barrier로 안전성을 보장한다.
- FFI 반환값 규약: `i32` 반환, 양수/0 = 성공, 음수 = `ErrorCode` 열거형
- 모든 FFI 구조체에 `#[repr(C)]` 사용; Rust 할당 메모리는 `hcv_*_free()` 함수로 해제
- 에러 코드 동기화: `vmcore/src/panic_barrier.rs::ErrorCode` ↔ `controller/pkg/ffi/errors.go` 상수
- CGo 빌드 태그: `cgo_vmcore` — `go build -tags cgo_vmcore ./pkg/ffi/...`
- 실제 CGo 링크 없이 테스트할 때는 `pkg/ffi/mock.go`의 `MockVMCore` 사용
- `build.rs` + `cbindgen`이 빌드 시 `vmcore.h` C 헤더를 자동 생성

### KVM 시스템 인터페이스 (kvm_sys.rs)

`/dev/kvm` ioctl을 안전한 Rust 추상화로 감싼 모듈. kvm_mgr.rs(인메모리 상태 머신)와 분리되어 있다.

- **`KvmSystem`**: /dev/kvm 열기 + API v12 검증 + 확장 확인 + `create_vm()`
- **`KvmVm`**: `set_user_memory_region()` (mmap 기반 페이지 정렬 메모리), `create_vcpu()`
- **`KvmVcpu`**: vCPU fd 관리 (KVM_RUN 준비)
- 에러 타입: `KvmSysError` (OpenFailed, ApiVersion, Ioctl, ExtensionMissing)
- 게스트 메모리는 반드시 `mmap(MAP_PRIVATE | MAP_ANONYMOUS)`로 페이지 정렬 할당해야 한다 (Vec 등 힙 할당은 EINVAL 발생)
- KVM 미사용 환경에서 테스트 자동 SKIP

```
kvm_mgr.rs  (인메모리 상태 머신 — FFI 레이어, 항상 사용)
kvm_sys.rs  (실제 /dev/kvm ioctl — 하이퍼바이저 연동 시 사용)
```

### vmcore Typestate 패턴 (vCPU 생명주기)

`vcpu_mgr.rs`는 제로 크기 마커 타입(`TCreated`, `TConfigured`, `TRunning`, `TPaused`)을 `VCpu<S>`의 제네릭 파라미터로 사용한다. 상태 전이 시 `self`를 소비하고 새로운 타입을 반환하므로, 잘못된 상태 전이는 컴파일 타임에 에러가 발생한다. FFI 런타임 레이어에서는 `VCpuEntry.state` 필드로 동적 검증을 수행한다.

### VM 상태 머신 (kvm_mgr.rs)

`VmState` 열거형과 `can_transition_to()` 메서드로 런타임 상태 전이를 검증한다. 허용되는 전이:
```
Created → Configured → Running ⇄ Paused
                    ↘ Stopped ↙
```

### 이벤트 링 (Lock-Free SPSC)

`event_ring.rs`는 단일 생산자(Rust) / 단일 소비자(Go) 링 버퍼를 힙 할당으로 구현한다. `AtomicU64`와 `Acquire`/`Release` 메모리 오더링으로 동기화하며, FFI를 통해 `EventRingHandle` 포인터를 Go에 노출한다.

### Dual VMM Backend Selector (Go Controller)

`internal/compute/`에 구현된 핵심 오케스트레이션 패턴 (ADR-006):

- **`VMCoreBackend` 인터페이스** (`pkg/ffi/mock.go`): 실제 CGo와 Mock 양쪽이 구현
- **`RustVMMBackend`** (`compute.go`): VMCoreBackend를 래핑하여 `VMMBackend` 인터페이스 구현. 고성능 Linux microVM 용도. Handle 범위: 1~9999
- **`QEMUBackend`** (`qemu_backend.go`): QMP(QEMU Machine Protocol) 기반 범용 VM 백엔드. Windows, GPU 패스스루, 레거시 OS 용도. Handle 범위: 10000+
  - `Emulated` 모드: 인메모리 상태 머신 (개발/테스트, QEMU 바이너리 불필요)
  - `Real` 모드: QMP unix socket으로 실제 qemu-system-x86_64 제어 (향후 구현)
  - QMP 명령 매핑: start→`cont`, stop→`system_powerdown`, pause→`stop`, resume→`cont`
- **`BackendSelector`**: 등록된 백엔드 중 워크로드 정책에 따라 선택
  - `Select(hint)`: 명시적 백엔드 지정 또는 기본값(rustvmm)
  - `SelectAuto(vcpus, memoryMB, needsGPU)`: 워크로드 기반 자동 선택 — GPU/대형VM(>8vCPU, >8GB)→QEMU, 경량→rustvmm
- **`ComputeService`**: VM CRUD + 생명주기 액션을 백엔드로 위임

API 라우터(`internal/api/router.go`)는 `Services` 구조체를 받아 live 모드로 동작하거나, `nil`을 받으면 스텁 모드로 동작한다. VM 생성 시 `backend` 필드로 백엔드를 명시할 수 있다.

### gRPC 서비스 레이어

`internal/grpcapi/`에 proto 정의를 내부 서비스로 연결하는 gRPC 서버 구현체가 있다:

| gRPC 서비스 | Proto 패키지 | RPC 수 | 내부 서비스 |
|-------------|-------------|--------|------------|
| `ComputeService` | `hardcorevisor.compute.v1` | 9 | `internal/compute` |
| `StorageAgent` | `hardcorevisor.storage.v1` | 5 | `internal/storage` |
| `PeripheralManager` | `hardcorevisor.peripheral.v1` | 3 | `internal/peripheral` |

- proto 원본은 `proto/` 디렉터리, 생성 코드는 `controller/pkg/proto/` — `just proto-gen`으로 재생성
- gRPC reflection 활성화 — `grpcurl -plaintext localhost:9090 list`로 탐색 가능
- Controller main.go에서 REST(:8080)와 gRPC(:9090)를 동시 서빙
- 환경변수: `HCV_GRPC_ADDR` (기본 `:9090`)

### etcd 상태 영속화

`internal/store/etcd.go`에 `Store` 인터페이스로 추상화:

- **`EtcdStore`**: etcd v3 클라이언트 기반 KV 저장소. `Put/Get/Delete/List` + JSON 직렬화
- **`MemoryStore`**: 인메모리 폴백 (etcd 미연결 시 자동 전환)
- **`NewStore(endpoints)`**: 팩토리 — 환경변수 `HCV_ETCD_ENDPOINTS` 설정 시 etcd, 미설정 시 메모리
- **`PersistentComputeService`** (`internal/compute/persistent.go`): ComputeService를 래핑하여 VM CRUD 시 자동으로 etcd에 저장. `LoadFromStore()`로 시작 시 복원
- **`ComputeProvider`** 인터페이스: ComputeService와 PersistentComputeService 양쪽이 구현. API/gRPC 레이어에서 투명하게 사용

### hcvctl CLI

`controller/cmd/hcvctl/main.go` — 전체 서비스 커버리지 CLI:

- 글로벌 플래그: `--api` (Controller 주소), `--output json|yaml|table` (`-o`), `--tls-skip-verify`, `--user/--password`
- `printOutput()` 헬퍼로 모든 list 커맨드가 json/yaml/table 출력 지원
- 서브커맨드: `vm` (list/create/start/stop), `node` (list), `storage` (pool list/volume list/create), `network` (zone/vnet/firewall list), `device` (list/attach/detach), `cluster` (status/node list/fence), `backup` (list/create/delete), `completion` (bash/zsh/fish)

### Grafana + Prometheus 모니터링

- **Grafana 대시보드**: `deploy/grafana/dashboards/hardcorevisor.json` — VM 상태, API 요청률, API 레이턴시, 노드 수, 스토리지 사용량 5패널. Docker 스택 시작 시 자동 프로비저닝
- **Prometheus 메트릭** (`/metrics`): `hcv_vms_total`, `hcv_api_requests_total`, `hcv_api_request_duration_seconds`, `hcv_nodes_total`, `hcv_storage_pool_bytes`
- **Alerting Rules** (`deploy/alert-rules.yml`): NodeDown(1분), StorageHigh(80%+, 5분), APIErrorRate(5xx, 2분), NoVMsRunning(5분)

### RBAC + 감사 로깅 (auth/)

`internal/auth/`에 보안 미들웨어 구현:

- **RBAC** (`rbac.go`): `admin`(전체 권한), `operator`(읽기+쓰기), `viewer`(읽기 전용) 3단계 역할
  - `HCV_RBAC_USERS` 환경변수: `user1:pass1:admin,user2:pass2:viewer` 형식
  - `/healthz`, `/metrics`는 인증 없이 접근 가능
  - 미설정 시 RBAC 비활성
- **감사 로깅** (`audit.go`): 모든 API 호출을 구조화 JSON으로 출력
  - `{"audit":true, "ts":"...", "user":"admin", "method":"POST", "path":"/api/v1/vms", "status":201, "duration_ms":1.2}`
- 미들웨어 체인: `RequestID → Audit → Logging → Metrics → RBAC → CORS → Recovery`

### Backup 서비스 (backup/)

`internal/backup/backup.go` — 스토리지 스냅샷 기반 VM 백업:

- `CreateBackup(vmID, vmName, pool)`: 볼륨 생성 + 스냅샷 생성 + 백업 레코드
- `ListBackups()`, `GetBackup(id)`, `DeleteBackup(id)`
- REST: `GET/POST /api/v1/backups`, `GET/DELETE /api/v1/backups/{id}`
- hcvctl: `backup list`, `backup create --vm-id --vm-name --pool`, `backup delete --id`

### Virtio 네트워크 디바이스 (virtio_net.rs)

`virtio_net.rs` — MAC 주소 관리, RX/TX 큐 기반 네트워크 에뮬레이션:

- `VirtioNetConfig`: MAC [u8;6], status, max_queue_pairs
- `VirtioNetStats`: rx/tx packets, bytes, drops
- FFI 8개: create, destroy, process_rx, process_tx, get_stats, set_mac, attach, detach

### Go Controller 서비스 레이어

`Services` 구조체를 통해 5개 서비스가 API 라우터에 연결된다:

| 서비스 | 패키지 | 기능 |
|--------|--------|------|
| **Compute** | `internal/compute` | VM CRUD, 생명주기, RustVMM + QEMU Dual Backend Selector |
| **Storage** | `internal/storage` | ZFS/Ceph 풀, 볼륨, 스냅샷 관리 |
| **Network** | `internal/network` | SDN 존, VNet, 방화벽 규칙 (VXLAN/nftables) |
| **Peripheral** | `internal/peripheral` | GPU/NIC/USB 패스스루, IOMMU 그룹 |
| **HA** | `internal/ha` | 클러스터 상태, quorum 계산, 펜싱 |

모든 서비스는 인메모리 구현으로 기본 데이터를 포함하며, mutex로 thread-safe하다.

### 미들웨어 체인

```
RequestID → Logging → CORS → Recovery → Handler
```

`X-Request-Id` 헤더를 자동 생성하며, CORS preflight(OPTIONS)를 처리하고, 패닉 복구로 서버 안정성을 보장한다.

### hcvtui 라이브 데이터 연동

TUI는 Go Controller REST API에 2초 간격으로 폴링하여 실시간 데이터를 표시한다:
- `app.rs`의 `tick()`: `tokio::join!`으로 VMs + Nodes 병렬 폴링
- 연결 상태 추적: `ConnStatus` (Connected/Disconnected/Error)
- `HCV_API_ADDR` 환경변수로 Controller 주소 설정 가능
- VM Manager 화면에서 직접 VM 제어: `s` start, `x` stop, `p` pause, `d` delete
- `c` 키로 VM 생성 인터랙티브 폼 (이름/vCPU/메모리/백엔드, Tab 이동, Enter 생성, Esc 취소)

### io_uring 비동기 I/O 엔진 (io_engine.rs)

Linux 6.x `io_uring` 기반 비동기 디스크 I/O 엔진. `io-uring` crate v0.7 사용.

```
virtio_blk (게스트 블록 요청) → io_engine (SQ/CQ) → 커널 io_uring → 디스크
```

- **`IoEngine`**: io_uring 인스턴스 관리. `new(queue_depth)` — 256 또는 1024 권장
- **`register_file(path)`**: 디스크 파일 등록 → fd_index 반환
- **`submit_read/write/flush`**: SQE 비동기 제출 (zero-copy)
- **`poll_completions`**: CQ에서 완료 수집 (비차단) / `wait_completions` (차단)
- **`IoCompletion`**: `{user_data, result, op}` — caller가 요청 ID와 완료를 매칭
- **`IoEngineStats`**: submitted, completed, inflight, ring_capacity, registered_files
- FFI 8개 함수: `hcv_io_engine_create/destroy/register_file/submit_read/submit_write/submit_flush/poll/stats`
- 게스트 블록 I/O 버퍼는 `submit`부터 `poll` 완료까지 유효해야 함 (lifetime 보장 필수)

### Virtio 디바이스 에뮬레이션

`virtio_split_queue.rs`(Virtio 1.x Split Virtqueue)와 `virtio_blk.rs`(블록 디바이스). 큐 크기는 반드시 2의 거듭제곱이어야 한다. Split Queue는 `AtomicU16`으로 avail/used 링의 lock-free 동기화를 수행한다.

## 사전 요구사항

- Rust 1.82+, Go 1.24+, `just`
- Proto: `protoc` 28+, `protoc-gen-go`, `protoc-gen-go-grpc` (`just proto-gen`으로 코드 생성)
- 선택: `cargo-nextest`, `cargo-watch`, `cargo-tarpaulin`, `cargo-audit`, `golangci-lint`, `govulncheck`, `cbindgen`, `grpcurl`
- KVM: `/dev/kvm` — 실제 하이퍼바이저 동작 및 kvm_sys 테스트에 필요 (없으면 자동 SKIP)
- io_uring: Linux 5.1+ 커널 — io_engine 모듈에 필요 (6.x 권장)
- Docker: 개발 서비스 실행용 (etcd :2379, Prometheus :9090, Grafana :3000 — `just dev-up`)
- etcd: VM 상태 영속화용 (선택 — 미연결 시 인메모리 폴백). `HCV_ETCD_ENDPOINTS=localhost:2379`

## 코드 스타일

- Rust: 4칸 들여쓰기, 100자 줄 제한, `cargo fmt` + `clippy -D warnings`
- Go: 탭 (gofmt), `golangci-lint`, 테스트 시 race detector 활성화 (`-race`)
- Proto: 2칸 들여쓰기
- `.editorconfig`로 에디터 설정 통일
- CI: `ubuntu-24.04`, 대상 브랜치 `main`/`develop`, 8-job 파이프라인: rust-lint → rust-test → rust-coverage(tarpaulin) → go-lint → go-test(coverprofile) → e2e → security(audit+govulncheck) → build-artifacts(릴리즈 바이너리)

## FFI 에러 코드 참조표

| 코드 | Rust (`ErrorCode`) | Go 상수 (`pkg/ffi/errors.go`) | 의미 |
|------|-------------------|-------------------------------|------|
| -1 | `Panic` | `ErrPanic` | FFI 경계에서 포착된 내부 패닉 |
| -2 | `InvalidArg` | `ErrInvalidArg` | 잘못된 인자 |
| -3 | `KvmError` | `ErrKVM` | KVM ioctl 실패 |
| -4 | `NotFound` | `ErrNotFound` | 리소스를 찾을 수 없음 |
| -5 | `InvalidState` | `ErrInvalidState` | 잘못된 상태 전이 |
| -6 | `OutOfMemory` | `ErrOutOfMemory` | 메모리 할당 실패 |
| -7 | `NotSupported` | `ErrNotSupported` | 지원되지 않는 작업 |
| -8 | `AlreadyExists` | — | 리소스가 이미 존재 |

## REST API 엔드포인트

기본 주소: `http://localhost:8080`

### Compute (VM 관리)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/healthz` | 헬스 체크 |
| GET | `/api/v1/version` | 버전 정보 (product, vmcore_version 등) |
| GET | `/api/v1/vms` | VM 목록 조회 |
| POST | `/api/v1/vms` | VM 생성 (`{name, vcpus, memory_mb, backend?}` → 201) |
| GET | `/api/v1/vms/{id}` | VM 상세 조회 (404 if not found) |
| DELETE | `/api/v1/vms/{id}` | VM 삭제 (204) |
| POST | `/api/v1/vms/{id}/start\|stop\|pause\|resume` | VM 생명주기 (409 if invalid state) |
| GET | `/api/v1/backends` | VMM 백엔드 목록 |

### Storage (스토리지)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/storage/pools` | 스토리지 풀 목록 (ZFS/Ceph) |
| GET | `/api/v1/storage/volumes?pool=` | 볼륨 목록 (풀 필터) |
| POST | `/api/v1/storage/volumes` | 볼륨 생성 (`{pool, name, size_bytes, format}`) |
| DELETE | `/api/v1/storage/volumes/{id}` | 볼륨 삭제 |

### Network (SDN)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/network/zones` | SDN 존 목록 (VXLAN/VLAN/Simple) |
| GET | `/api/v1/network/vnets?zone=` | 가상 네트워크 목록 |
| GET | `/api/v1/network/firewall` | 방화벽 규칙 목록 |

### Peripheral (디바이스 패스스루)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/devices?type=` | 디바이스 목록 (gpu/nic/usb 필터) |
| POST | `/api/v1/devices/{id}/attach` | VM에 디바이스 연결 (`{vm_handle}`) |
| POST | `/api/v1/devices/{id}/detach` | 디바이스 분리 |

### HA (클러스터)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/cluster/status` | 클러스터 상태 (quorum, leader, health) |
| GET | `/api/v1/cluster/nodes` | HA 노드 목록 |
| POST | `/api/v1/cluster/fence/{node}` | 펜싱 실행 (`{reason, action}`) |
| GET | `/api/v1/nodes` | 노드 리소스 현황 (스텁) |

### Backup (백업)

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/backups` | 백업 목록 |
| POST | `/api/v1/backups` | 백업 생성 (`{vm_id, vm_name, pool}`) |
| GET | `/api/v1/backups/{id}` | 백업 상세 조회 |
| DELETE | `/api/v1/backups/{id}` | 백업 삭제 |

### Monitoring

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/metrics` | Prometheus 메트릭 (hcv_vms_total, hcv_api_requests_total 등) |

## 라이선스

AGPL-3.0
