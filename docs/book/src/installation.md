# 설치 가이드

## 의존성

### 필수

| 도구 | 버전 | 용도 |
|------|------|------|
| **Rust** | 1.82+ | vmcore, hcvtui 빌드 |
| **Go** | 1.23+ | Controller, hcvctl 빌드 |
| **just** | 최신 | 통합 빌드 시스템 (커맨드 러너) |
| **Linux 커널** | 5.1+ (6.x 권장) | io_uring 지원 |

### 선택

| 도구 | 용도 |
|------|------|
| **protoc** 28+ | gRPC proto 코드 생성 |
| **protoc-gen-go** | Go protobuf 코드 생성 |
| **protoc-gen-go-grpc** | Go gRPC 코드 생성 |
| **Docker** + **Docker Compose** | 개발 서비스 (etcd, Prometheus, Grafana) |
| **/dev/kvm** | 실제 하이퍼바이저 동작 (없으면 자동 SKIP) |
| **cargo-nextest** | 병렬 테스트 러너 |
| **cargo-watch** | 파일 변경 감지 자동 빌드 |
| **cargo-tarpaulin** | Rust 코드 커버리지 |
| **cargo-audit** | Rust 보안 취약점 검사 |
| **cbindgen** | C 헤더 자동 생성 |
| **golangci-lint** | Go 린트 |
| **govulncheck** | Go 보안 취약점 검사 |
| **grpcurl** | gRPC CLI 테스트 도구 |

## 빌드 방법

### 전체 빌드

```bash
git clone https://github.com/HardcoreMonk/hardcorevisor.git
cd hardcorevisor

# Go 모듈 초기화 (최초 1회)
just go-init

# 전체 빌드 (Rust + Go)
just build

# 릴리즈 빌드
just release
```

### 개별 빌드

```bash
# Rust만 빌드
just rust-build

# Rust 릴리즈 빌드
just rust-release

# Go만 빌드
just go-build

# Go Controller 바이너리
just go-controller

# hcvctl CLI 바이너리 (버전 정보 주입)
just go-hcvctl
```

### 릴리즈 바이너리

```bash
just release-build
# 결과물:
#   target/release/libvmcore.a  (Rust staticlib)
#   target/controller           (Go Controller)
#   target/hcvctl               (CLI)
```

## Docker 배포

### 개발 스택

```bash
# etcd + Prometheus + Grafana + Controller 시작
just dev-up

# 서비스 확인
#   etcd:       localhost:2379
#   Prometheus: localhost:9090
#   Grafana:    localhost:3000
#   Controller: localhost:8080

# 서비스 중지
just dev-down
```

### Docker 이미지 빌드

```bash
# Docker 이미지 빌드
just docker-build

# 스택 스모크 테스트 (빌드 + 시작 + 테스트 + 종료)
sudo just stack-test
```

### Dockerfile

Controller는 멀티스테이지 빌드로 distroless 이미지를 사용한다:

```
deploy/Dockerfile.controller    # Go 멀티스테이지 빌드
deploy/docker-compose.yml       # 전체 스택 정의
```

## 개발 환경 검증

```bash
# 개발 환경 자동 검증
./scripts/setup-dev.sh

# KVM 지원 확인
ls -la /dev/kvm
```
