# 빠른 시작

## Prerequisites

- **Rust** 1.82+
- **Go** 1.23+
- **just** (커맨드 러너)
- **/dev/kvm** (선택 - 실제 하이퍼바이저 동작에 필요, 없으면 자동 SKIP)
- **Linux 5.1+** 커널 (io_uring 지원, 6.x 권장)

## 소스 클론 및 빌드

```bash
git clone https://github.com/HardcoreMonk/hardcorevisor.git
cd hardcorevisor

# 전체 빌드 (Rust + Go)
just build

# 전체 테스트 (Rust 70 + Go 15 = 85 tests)
just test
```

## Controller + TUI 실행

두 개의 터미널이 필요하다:

```bash
# 터미널 1: Go Controller 시작 (REST :18080 + gRPC :19090)
just go-run

# 터미널 2: TUI 라이브 대시보드
just tui
```

TUI가 Controller에 2초 간격으로 폴링하여 실시간 데이터를 표시한다.

## Docker 스택

etcd, Prometheus, Grafana를 포함한 전체 개발 스택을 실행할 수 있다:

```bash
# 개발 서비스 시작 (etcd :2379, Prometheus :9090, Grafana :3000)
just dev-up

# 서비스 로그 확인
just dev-logs

# 서비스 중지
just dev-down
```

## 빠른 검증

```bash
# 빠른 프리커밋 검사 (<30초)
just quick

# 전체 lint + test
just check

# 사용 가능한 모든 명령어 보기
just --list
```

## REST API 테스트

Controller 실행 후:

```bash
# 헬스 체크
curl -s localhost:18080/healthz | jq

# VM 생성
curl -s -X POST localhost:18080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-vm","vcpus":2,"memory_mb":4096}' | jq

# VM 목록
curl -s localhost:18080/api/v1/vms | jq
```

## hcvctl CLI

```bash
# CLI 바이너리 빌드
just go-hcvctl

# VM 목록 (JSON 출력)
hcvctl vm list -o json | jq '.[].name'

# 클러스터 상태
hcvctl cluster status
```
