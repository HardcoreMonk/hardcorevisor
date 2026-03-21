# TUI 사용법

hcvtui는 Ratatui 프레임워크로 작성된 터미널 기반 라이브 대시보드이다. Go Controller의 REST API에 2초 간격으로 폴링하여 실시간 데이터를 표시한다.

## 실행 방법

```bash
# Go Controller 시작 (터미널 1)
just go-run

# TUI 실행 (터미널 2)
just tui

# 또는 직접 실행
cargo run -p hcvtui

# Controller 주소 지정
HCV_API_ADDR=192.168.1.100:8080 cargo run -p hcvtui

# 파일 변경 감지 자동 재시작
just rust-watch-tui
```

TUI는 Controller에 연결되면 `Connected` 상태를 표시하며, 연결 실패 시 `Disconnected` 또는 `Error` 상태를 표시한다.

## 키바인딩

### 전체 화면 공통

| 키 | 동작 |
|----|------|
| `1` | Dashboard 화면으로 전환 |
| `2` | VM Manager 화면으로 전환 |
| `3` | Storage 화면으로 전환 |
| `4` | Network 화면으로 전환 |
| `5` | Logs 화면으로 전환 |
| `6` | HA 화면으로 전환 |
| `j` 또는 `Down` | 목록 아래로 스크롤 |
| `k` 또는 `Up` | 목록 위로 스크롤 |
| `r` | 수동 새로고침 |
| `q` | TUI 종료 |

### VM Manager 화면 전용

| 키 | 동작 |
|----|------|
| `s` | 선택된 VM 시작 |
| `x` | 선택된 VM 중지 |
| `p` | 선택된 VM 일시정지 |
| `d` | 선택된 VM 삭제 |
| `c` | VM 생성 폼 열기 |
| `Enter` | VM 상세 뷰 팝업 |

### VM 생성 폼

| 키 | 동작 |
|----|------|
| `Tab` | 다음 입력 필드로 이동 |
| `Shift+Tab` | 이전 입력 필드로 이동 |
| `Enter` | VM 생성 실행 |
| `Esc` | 폼 닫기 (취소) |

## 화면별 설명

### 1. Dashboard

전체 시스템 현황을 한눈에 보여주는 메인 화면이다. VM 수, 노드 상태, API 연결 상태 등을 요약하여 표시한다.

### 2. VM Manager

VM 목록을 테이블로 표시하며, 직접 VM을 제어할 수 있는 주요 작업 화면이다.

- VM 이름, vCPU, 메모리, 상태, 백엔드 정보를 표시
- `j`/`k`로 VM을 선택하고 `s`/`x`/`p`/`d`로 제어
- `Enter`로 상세 뷰 팝업을 열어 VM의 전체 정보를 확인
- `c`로 VM 생성 폼을 열어 새 VM을 만들 수 있다

### 3. Storage

스토리지 풀과 볼륨 정보를 표시한다. ZFS, Ceph 등 풀 유형별로 용량과 사용량을 확인할 수 있다.

### 4. Network

SDN 존, 가상 네트워크(VNet), 방화벽 규칙을 표시한다. VXLAN/VLAN/Simple 존 유형과 네트워크 구성을 확인할 수 있다.

### 5. Logs

시스템 로그를 실시간으로 표시한다. 최근 이벤트와 에러를 추적할 수 있다.

### 6. HA

클러스터 고가용성 상태를 표시한다. 노드 상태, quorum 정보, 리더 노드, 클러스터 헬스를 확인할 수 있다.

## VM 생성 폼

`c` 키를 누르면 인터랙티브 VM 생성 폼이 열린다:

- **이름**: VM 이름 (문자열)
- **vCPU**: 가상 CPU 수 (정수)
- **메모리 (MB)**: 메모리 크기 (정수)
- **백엔드**: VMM 백엔드 선택 (rustvmm 또는 qemu, 비워두면 자동 선택)

`Tab`으로 필드를 이동하고, `Enter`로 생성을 실행한다. `Esc`로 취소할 수 있다.

## VM 상세 뷰

VM 목록에서 `Enter`를 누르면 팝업으로 상세 정보를 표시한다:

- VM ID (Handle)
- 이름
- vCPU 수
- 메모리 크기
- 현재 상태
- VMM 백엔드 (rustvmm/qemu)

## 환경변수

| 환경변수 | 기본값 | 설명 |
|----------|--------|------|
| `HCV_API_ADDR` | `localhost:8080` | Controller REST API 주소 |
