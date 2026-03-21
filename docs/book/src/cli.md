# hcvctl CLI

hcvctl은 HardCoreVisor의 명령행 인터페이스 도구이다. Go Cobra 프레임워크로 작성되었다.

## 설치

```bash
# 소스에서 빌드 (버전 정보 자동 주입)
just go-hcvctl

# 빌드된 바이너리 위치
ls -la target/hcvctl
```

빌드 시 `-ldflags`로 다음 정보가 자동 주입된다:
- `version`: git describe --tags --always
- `commit`: git rev-parse --short HEAD
- `buildDate`: 빌드 시각 (UTC)

## 글로벌 플래그

| 플래그 | 단축 | 기본값 | 설명 |
|--------|------|--------|------|
| `--api` | | `localhost:8080` | Controller API 주소 |
| `--output` | `-o` | `table` | 출력 포맷 (json, yaml, table) |
| `--tls-skip-verify` | | `false` | TLS 인증서 검증 건너뛰기 |
| `--user` | | | Basic Auth 사용자명 |
| `--password` | | | Basic Auth 비밀번호 |

## 서브커맨드 전체 목록

### vm (VM 관리)

```bash
hcvctl vm list                    # VM 목록
hcvctl vm create --name test \
  --vcpus 2 --memory 4096        # VM 생성
hcvctl vm start --id 1           # VM 시작
hcvctl vm stop --id 1            # VM 중지
```

### node (노드)

```bash
hcvctl node list                  # 노드 목록
```

### storage (스토리지)

```bash
hcvctl storage pool list          # 스토리지 풀 목록
hcvctl storage volume list        # 볼륨 목록
hcvctl storage volume create \
  --pool local-zfs --name disk-01 \
  --size 10737418240              # 볼륨 생성
```

### network (네트워크)

```bash
hcvctl network zone list          # SDN 존 목록
hcvctl network vnet list          # VNet 목록
hcvctl network firewall list      # 방화벽 규칙 목록
```

### device (디바이스)

```bash
hcvctl device list                # 디바이스 목록
hcvctl device attach --id gpu-0 \
  --vm-handle 1                   # 디바이스 연결
hcvctl device detach --id gpu-0   # 디바이스 분리
```

### cluster (클러스터)

```bash
hcvctl cluster status             # 클러스터 상태
hcvctl cluster node list          # 클러스터 노드 목록
hcvctl cluster fence node-03 \
  --reason unresponsive \
  --action reboot                 # 펜싱 실행
```

### backup (백업)

```bash
hcvctl backup list                         # 백업 목록
hcvctl backup create --vm-id 1 \
  --vm-name web-01 --pool local-zfs        # 백업 생성
hcvctl backup delete --id backup-1         # 백업 삭제
```

### vm migrate (VM 마이그레이션)

```bash
hcvctl vm migrate --id 1 --target node-02   # VM을 대상 노드로 라이브 마이그레이션
```

### shell (인터랙티브 REPL)

```bash
hcvctl shell                              # 인터랙티브 REPL 모드 진입
# hcv> vm list
# hcv> cluster status
```

### status (시스템 통계)

```bash
hcvctl status                             # 시스템 전체 통계 (VM/스토리지/노드/업타임)
hcvctl status -o json                     # JSON 출력
```

### completion (쉘 자동 완성)

```bash
hcvctl completion bash            # Bash 완성 스크립트
hcvctl completion zsh             # Zsh 완성 스크립트
hcvctl completion fish            # Fish 완성 스크립트
```

## --output 포맷

모든 list 커맨드는 `--output` (`-o`) 플래그로 출력 포맷을 지정할 수 있다.

### table (기본)

```bash
hcvctl vm list
# NAME       VCPUS   MEMORY   STATE     BACKEND
# test-vm    2       4096     running   rustvmm
# win-srv    8       32768    stopped   qemu
```

### json

```bash
hcvctl vm list -o json | jq '.[].name'
# "test-vm"
# "win-srv"
```

### yaml

```bash
hcvctl storage pool list -o yaml
```

## TLS 사용

```bash
# TLS 인증서 검증 건너뛰기 (자체 서명 인증서)
hcvctl --tls-skip-verify vm list

# TLS + Basic Auth
hcvctl --tls-skip-verify --user admin --password secret vm list
```

## 쉘 자동 완성 설치

### Bash

```bash
# 현재 세션
source <(hcvctl completion bash)

# 영구 설치
hcvctl completion bash > /etc/bash_completion.d/hcvctl
```

### Zsh

```bash
# 현재 세션
source <(hcvctl completion zsh)

# 영구 설치
hcvctl completion zsh > "${fpath[1]}/_hcvctl"
```

### Fish

```bash
hcvctl completion fish | source

# 영구 설치
hcvctl completion fish > ~/.config/fish/completions/hcvctl.fish
```
