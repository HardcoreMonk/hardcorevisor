# 개발 가이드

## 빌드 시스템 (justfile)

HardCoreVisor는 `just` 커맨드 러너를 사용한다. 모든 명령어는 프로젝트 루트에서 실행한다.

```bash
# 사용 가능한 모든 명령어 보기
just --list
```

### 주요 명령어

| 명령어 | 설명 |
|--------|------|
| `just build` | 전체 빌드 (Rust + Go) |
| `just test` | 전체 테스트 |
| `just check` | lint + test (프리커밋) |
| `just quick` | 빠른 프리커밋 (<30초) |
| `just lint` | 전체 린트 |
| `just release` | 릴리즈 바이너리 빌드 |
| `just clean` | 빌드 아티팩트 삭제 |

### Rust 명령어

| 명령어 | 설명 |
|--------|------|
| `just rust-build` | Rust 워크스페이스 빌드 |
| `just rust-test` | 전체 Rust 테스트 (직렬) |
| `just rust-test-vmcore` | vmcore 테스트만 |
| `just rust-test-kvm` | KVM ioctl 테스트 (/dev/kvm 필요) |
| `just rust-test-mod <module>` | 특정 모듈 테스트 |
| `just rust-clippy` | Clippy 린트 (-D warnings) |
| `just rust-fmt` | 포맷 검사 |
| `just rust-fmt-fix` | 포맷 자동 수정 |
| `just rust-watch-vmcore` | 변경 감지 자동 테스트 |
| `just rust-coverage` | 코드 커버리지 (tarpaulin) |
| `just tui` | TUI 실행 |

### Go 명령어

| 명령어 | 설명 |
|--------|------|
| `just go-build` | Go 전체 빌드 |
| `just go-test` | Go 전체 테스트 (race detector) |
| `just go-test-e2e` | E2E 테스트만 |
| `just go-test-api` | API 유닛 테스트만 |
| `just go-run` | Controller 실행 |
| `just go-hcvctl` | hcvctl CLI 빌드 |
| `just go-lint` | golangci-lint |
| `just go-vet` | go vet |
| `just go-fmt` | gofmt |
| `just go-coverage` | Go 코드 커버리지 |

## 테스트 실행

### 전체 테스트

```bash
# Rust 70 + Go 15 = 85 tests
just test

# 전체 검증 (lint + test)
just check
```

### Rust 테스트

```bash
# vmcore 전체 (70개, 직렬 실행)
just rust-test-vmcore

# 특정 모듈
just rust-test-mod kvm_mgr
just rust-test-mod vcpu_mgr
just rust-test-mod memory_mgr
just rust-test-mod event_ring
just rust-test-mod io_engine
just rust-test-mod virtio_blk
just rust-test-mod virtio_net
just rust-test-mod panic_barrier

# 특정 테스트 하나
cargo test -p vmcore test_full_lifecycle -- --test-threads=1

# KVM 테스트 (실제 /dev/kvm 필요, 없으면 자동 SKIP)
just rust-test-kvm
```

### Go 테스트

```bash
# 전체 (race detector, 15개)
just go-test

# API 유닛 테스트 (3개)
just go-test-api

# E2E 통합 테스트 (12개)
just go-test-e2e
```

### E2E 통합 테스트

```bash
# 전체 E2E 스위트
just e2e

# Docker 서비스 포함
just e2e-stack

# Rust만 / Go만
just e2e-rust
just e2e-go
```

E2E 테스트 스택: `MockVMCore` -> `RustVMMBackend` -> `BackendSelector` -> `ComputeService` + Services -> `api.NewRouter(svc)` -> `httptest.Server`

## 벤치마크

### Rust 벤치마크

```bash
just bench
# criterion 4개: event_ring, VM CRUD, io_uring I/O, virtio split queue
```

### Go 벤치마크

```bash
just go-bench
# 3개: healthz, create VM, list VMs
```

## CI 파이프라인

`.github/workflows/ci.yml`에 정의된 8-job 파이프라인:

```
rust-lint → rust-test → rust-coverage (tarpaulin)
go-lint   → go-test   (coverprofile)
                      ↘ e2e → build-artifacts
security (audit + govulncheck)
```

| Job | 내용 |
|-----|------|
| **rust-lint** | `cargo fmt --check` + `cargo clippy -D warnings` |
| **rust-test** | `cargo test --workspace` + release 빌드 |
| **rust-coverage** | `cargo tarpaulin --workspace --out xml` |
| **go-lint** | `go vet` + `golangci-lint` |
| **go-test** | `go test -race -coverprofile=coverage.out` |
| **e2e** | vmcore 빌드 + Go E2E 테스트 |
| **security** | `cargo audit` + `govulncheck` |
| **build-artifacts** | 릴리즈 바이너리 빌드 + 아티팩트 업로드 |

## 코드 스타일

### Rust

- 4칸 들여쓰기
- 100자 줄 제한
- `cargo fmt` + `cargo clippy -D warnings` 통과 필수
- 모든 `extern "C"` FFI 함수는 `panic_barrier::catch()`로 래핑
- FFI 구조체에 `#[repr(C)]` 사용

### Go

- 탭 들여쓰기 (gofmt 표준)
- `golangci-lint` 통과
- 테스트 시 race detector 활성화 (`-race`)
- 구조화 로깅 (`log/slog`)

### Proto

- 2칸 들여쓰기

### 에디터 설정

`.editorconfig`로 에디터 설정이 통일되어 있다.

## 커밋 규칙

### 커밋 메시지 형식

커밋 메시지는 변경 사항의 "왜"에 초점을 맞춘다:

```
<type>: <subject>

<body>
```

### 타입

| 타입 | 설명 |
|------|------|
| `feat` | 새로운 기능 |
| `fix` | 버그 수정 |
| `refactor` | 리팩토링 |
| `test` | 테스트 추가/수정 |
| `docs` | 문서 변경 |
| `ci` | CI 파이프라인 변경 |
| `chore` | 기타 (의존성 업데이트 등) |

### 프리커밋 검사

커밋 전에 반드시 실행:

```bash
# 빠른 검사 (<30초)
just quick

# 또는 전체 검사
just check
```

## 프로젝트 구조

```
hardcorevisor/
├── vmcore/          # Rust staticlib (KVM 코어, 63 FFI)
├── hcvtui/          # Rust TUI (Ratatui)
├── controller/      # Go Controller (REST + gRPC)
├── proto/           # gRPC proto 정의
├── deploy/          # Docker + Grafana + Prometheus
├── scripts/         # 개발/테스트 스크립트
├── docs/            # 문서 (OpenAPI, mdbook)
├── Cargo.toml       # Rust 워크스페이스
├── justfile         # 통합 빌드 시스템
└── hcv.example.yaml # 설정 예제
```

## 드라이버 패턴 구현 방법

새 드라이버를 추가하려면 해당 서비스의 `Driver` 인터페이스를 구현한다. 예: `internal/storage/driver_ceph.go`에 `CephDriver` 구조체를 생성하고 `StorageDriver` 인터페이스의 모든 메서드를 구현한 뒤, `config.go`에서 환경변수로 선택 가능하도록 등록한다.

## Helm 차트

```bash
# Helm으로 배포
helm install hcv deploy/helm/
helm upgrade hcv deploy/helm/ --set controller.replicas=3
```

## 부하 테스트 / 벤치마크

```bash
just load-test    # API 부하 테스트 (healthz, VM CRUD, 동시 생성)
just bench        # Rust criterion 벤치마크 (event_ring, VM, io_uring, virtqueue)
just go-bench     # Go API 벤치마크 (healthz, create VM, list VMs)
```

## 코드 커버리지

```bash
# Rust 커버리지 (HTML 리포트)
just rust-coverage
# 결과: target/coverage/

# Go 커버리지 (HTML 리포트)
just go-coverage
# 결과: controller/coverage.html
```
