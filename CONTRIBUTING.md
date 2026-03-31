# Contributing to HardCoreVisor

## Prerequisites

- Rust 1.82+
- Go 1.24+
- [just](https://github.com/casey/just) command runner

Optional: `cargo-nextest`, `golangci-lint`, `grpcurl`, `helm`

## Quick Start

```bash
# Pre-commit hook 설치 (필수)
just hooks

# 전체 빌드
just build

# 전체 테스트 (Rust 82 + Go 111 = 193)
just test

# 빠른 프리커밋 (<30초, 8단계)
just quick
```

## Development Workflow

1. `develop` 브랜치에서 feature 브랜치 생성
2. 코드 작성 및 테스트
3. `just check` 실행 (lint + test)
4. PR 생성 — CI가 자동으로 검증

## Code Style

### Rust
- 4칸 들여쓰기, 100자 줄 제한
- `cargo fmt` + `cargo clippy -- -D warnings`
- 한국어 주석 (주니어 온보딩 목적)

### Go
- `gofmt` (탭 들여쓰기)
- 테스트 시 race detector 활성화 (`-race`)
- `golangci-lint run --fast`

### FFI Rules
- 모든 `extern "C"` 함수는 반드시 `panic_barrier::catch()`로 래핑
- FFI 반환값: 양수/0 = 성공, 음수 = ErrorCode
- 에러 코드: `vmcore/src/panic_barrier.rs` <-> `controller/pkg/ffi/errors.go` 동기화

## Testing

```bash
just rust-test-vmcore     # Rust vmcore 82개
just go-test              # Go 전체 (race detector)
just go-test-e2e          # E2E 통합 35개
just bench                # Rust criterion 벤치마크
```

## CGo Build (실제 FFI 링크)

```bash
cargo build -p vmcore --release
cd controller && go build -tags cgo_vmcore ./pkg/ffi/...
```

## Project Structure

자세한 구조는 [CLAUDE.md](CLAUDE.md) 참조.
개발 가이드는 `docs/book/src/development.md` 참조.

## License

AGPL-3.0
