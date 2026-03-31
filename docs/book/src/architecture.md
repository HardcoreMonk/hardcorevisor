# 아키텍처

## 전체 아키텍처 다이어그램

```
┌──────────────────────────────────────────────────────┐
│  hcvtui (Rust/Ratatui)  │  Web UI (React)           │  ← Management Layer
├──────────────────────────────────────────────────────┤
│  hcvctl (Go/Cobra)      │  REST + gRPC API          │  ← API Layer
├──────────────────────────────────────────────────────┤
│  Go Controller           REST :18080 / gRPC :19090     │
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
│  vmcore (Rust staticlib) — 63 FFI functions          │
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

## 레이어 설명

### Management Layer (관리 계층)

사용자가 직접 상호작용하는 인터페이스 계층이다.

- **hcvtui** (Rust/Ratatui): 터미널 기반 라이브 대시보드. REST API에 2초 간격 폴링으로 실시간 데이터를 표시한다.
- **Web UI** (React): 웹 기반 관리 인터페이스 (향후 구현).

### API Layer (API 계층)

외부 시스템과의 통신을 담당한다.

- **hcvctl** (Go/Cobra): CLI 도구. `--output json|yaml|table` 출력 포맷 지원.
- **REST API** (:18080): OpenAPI 3.0 기반 28개 엔드포인트.
- **gRPC API** (:19090): 3개 서비스, 17개 RPC. Reflection 지원.

### Orchestration Layer (오케스트레이션 계층)

Go Controller가 VM 생명주기와 인프라 리소스를 관리한다.

| 서비스 | 패키지 | 기능 |
|--------|--------|------|
| Compute | `internal/compute` | VM CRUD, Dual VMM Backend Selector |
| Storage | `internal/storage` | ZFS/Ceph 풀, 볼륨, 스냅샷 |
| Network | `internal/network` | SDN 존, VNet, 방화벽 (VXLAN/nftables) |
| Peripheral | `internal/peripheral` | GPU/NIC/USB 패스스루, IOMMU |
| HA | `internal/ha` | 클러스터 상태, quorum, 펜싱 |

### KVM Core (vmcore)

Rust로 작성된 KVM 가상화 코어. C staticlib (`libvmcore.a`)로 컴파일되어 CGo를 통해 Go에 링크된다.

주요 모듈:

| 모듈 | 기능 |
|------|------|
| `kvm_mgr` | VM 레지스트리, 상태 머신 (VmState) |
| `kvm_sys` | /dev/kvm ioctl 래퍼 |
| `kvm_boot` | x86 미니 게스트 부팅 |
| `vcpu_mgr` | Typestate vCPU 생명주기 |
| `memory_mgr` | 게스트 메모리 영역, dirty log |
| `event_ring` | Lock-free SPSC 이벤트 버스 |
| `io_engine` | io_uring 비동기 디스크 I/O |
| `virtio_blk` | Virtio 블록 디바이스 에뮬레이션 |
| `virtio_net` | Virtio 네트워크 디바이스 에뮬레이션 |
| `panic_barrier` | catch_unwind FFI 안전성 래퍼 |

## FFI 경계 (Rust → Go)

vmcore는 총 63개의 `extern "C"` FFI 함수를 노출한다. 핵심 규칙:

1. **모든 FFI 함수는 `panic_barrier::catch()`로 래핑** — Rust 패닉이 Go 런타임으로 전파되는 것을 방지.
2. **반환값 규약**: `i32` 반환, 양수/0 = 성공, 음수 = `ErrorCode` 열거형.
3. **모든 FFI 구조체에 `#[repr(C)]` 사용** — C ABI 호환성 보장.
4. **Rust 할당 메모리는 `hcv_*_free()` 함수로 해제** — 메모리 누수 방지.
5. **`build.rs` + `cbindgen`**이 빌드 시 `vmcore.h` C 헤더를 자동 생성.

```
Go Controller
    ↓ CGo (cgo_vmcore 빌드 태그)
    ↓ pkg/ffi/vmcore.go (실제 바인딩) 또는 pkg/ffi/mock.go (테스트용 Mock)
    ↓
vmcore (libvmcore.a)
    ↓ panic_barrier::catch()
    ↓ 내부 Rust 모듈
```

## Dual VMM 아키텍처

Go Controller의 `internal/compute/`에 구현된 Backend Selector 패턴:

- **RustVMMBackend**: VMCoreBackend를 래핑. Handle 범위 1~9999. 고성능 Linux microVM.
- **QEMUBackend**: QMP(QEMU Machine Protocol) 기반. Handle 범위 10000+. Windows, GPU 패스스루, 레거시 OS.
  - `Emulated` 모드: 인메모리 상태 머신 (개발/테스트용)
  - `Real` 모드: QMP unix socket으로 실제 QEMU 제어

**BackendSelector 자동 선택 기준:**

```
GPU 필요 → QEMU
vCPU > 8 또는 메모리 > 8GB → QEMU
그 외 → rust-vmm
```

## io_uring 비동기 I/O

Linux 6.x `io_uring` 기반 비동기 디스크 I/O 엔진. 게스트 블록 I/O 요청을 zero-copy로 처리한다.

```
virtio_blk (게스트 블록 요청) → io_engine (SQ/CQ) → 커널 io_uring → 디스크
```

## VM 상태 머신

허용되는 상태 전이:

```
Created → Configured → Running ⇄ Paused
                    ↘ Stopped ↙
```

잘못된 상태 전이 시도 시 REST API는 409 Conflict를 반환한다.

## 플러그어블 드라이버 패턴

Storage, Network, Peripheral, HA 4개 서비스 모두 `Driver` 인터페이스를 정의하고, 인메모리(dev/test)와 실제 백엔드(ZFS, nftables, sysfs, etcd)를 환경변수로 전환한다. 새 드라이버는 인터페이스만 구현하면 무중단으로 교체할 수 있다.

## VFIO 패스스루 워크플로

`internal/peripheral/driver_sysfs.go`가 `/sys/bus/pci/devices/`를 스캔하여 IOMMU 그룹별로 디바이스를 분류한다. Attach 시 vfio-pci 드라이버를 바인딩하고 VM에 할당, Detach 시 원래 드라이버로 복원한다.

## KVM Dirty Log (마이그레이션)

`memory_mgr.rs`의 `dirty log` 기능을 통해 게스트 메모리 변경 페이지를 추적한다. 라이브 마이그레이션 시 변경된 페이지만 대상 노드로 전송하여 다운타임을 최소화한다.

## io_uring 파이프라인

`virtio_blk_io.rs`가 virtio-blk 요청을 `io_engine.rs`의 io_uring SQ에 제출하고, CQ에서 완료를 수집하여 게스트에 응답한다. zero-copy 경로로 커널 컨텍스트 스위칭을 최소화한다.
