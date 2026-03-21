# HardCoreVisor 소개

> **"타협 없는 성능·보안·안정성의 하이퍼바이저 감독자"**

HardCoreVisor는 VMware와 Proxmox 두 플랫폼의 모든 강점을 통합한 차세대 하이브리드 가상화 플랫폼이다.

## Dual VMM 아키텍처

HardCoreVisor의 핵심은 **QEMU(범용) + rust-vmm(고성능) Dual VMM 아키텍처**이다. 워크로드에 따라 최적화된 VMM을 자동 선택한다.

| VMM | 용도 | 특징 |
|-----|------|------|
| **rust-vmm** | 고성능 Linux microVM | 경량, 빠른 부팅, 낮은 오버헤드 |
| **QEMU** | 범용 VM (Windows, GPU 패스스루, 레거시) | 폭넓은 디바이스 지원, QMP 제어 |

**BackendSelector**가 vCPU 수, 메모리 크기, GPU 필요 여부 등을 분석하여 적합한 백엔드를 자동 선택한다:
- GPU 필요 또는 대형 VM (>8 vCPU, >8GB 메모리) → QEMU
- 경량 워크로드 → rust-vmm

## 주요 기능

### 가상화 코어 (vmcore)
- Rust로 작성된 KVM 코어 staticlib (63개 FFI 함수)
- Typestate 패턴 기반 vCPU 생명주기 관리
- Lock-free SPSC 이벤트 링 버퍼
- io_uring 기반 비동기 디스크 I/O
- Virtio 디바이스 에뮬레이션 (블록, 네트워크)
- panic_barrier로 FFI 안전성 보장

### 오케스트레이션 (Go Controller)
- REST API (:8080) + gRPC (:9090) 동시 서빙
- Dual VMM Backend Selector (자동/수동)
- etcd 기반 상태 영속화 (인메모리 폴백)
- 스토리지 관리 (ZFS/Ceph 풀, 볼륨, 스냅샷)
- SDN 네트워크 (VXLAN/nftables)
- GPU/NIC/USB 디바이스 패스스루
- HA 클러스터 관리 (quorum, 펜싱)
- VM 백업 서비스

### 관리 인터페이스
- **hcvtui**: Ratatui 기반 터미널 UI (라이브 대시보드)
- **hcvctl**: Cobra 기반 CLI (json/yaml/table 출력)
- **REST API**: OpenAPI 3.0 스펙 기반 28개 엔드포인트
- **gRPC API**: 3개 서비스, 17개 RPC (reflection 지원)

### 운영
- Prometheus 메트릭 + Grafana 대시보드
- RBAC 3단계 역할 (admin/operator/viewer)
- 감사 로깅 (구조화 JSON)
- TLS 지원
- Docker Compose 기반 개발 스택

## 기술 스택

| 계층 | 기술 |
|------|------|
| KVM 코어 | Rust, io_uring, Virtio |
| 오케스트레이션 | Go, gRPC, etcd |
| TUI | Rust, Ratatui, tokio |
| CLI | Go, Cobra |
| 모니터링 | Prometheus, Grafana |
| 배포 | Docker, Docker Compose |

## 라이선스

AGPL-3.0
