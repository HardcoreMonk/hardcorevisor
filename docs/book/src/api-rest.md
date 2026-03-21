# REST API

기본 주소: `http://localhost:8080`

## OpenAPI 스펙

전체 REST API 문서는 OpenAPI 3.0.3 스펙으로 제공된다:

- 파일: [`docs/openapi.yaml`](https://github.com/HardcoreMonk/hardcorevisor/blob/main/docs/openapi.yaml)
- 25개 경로, 모든 요청/응답 스키마, HTTP 상태 코드 포함
- Swagger UI 또는 Redoc으로 렌더링 가능

## 엔드포인트 전체 목록

### Compute (VM 관리) - 10개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/healthz` | 헬스 체크 |
| GET | `/api/v1/version` | 버전 정보 |
| GET | `/api/v1/vms` | VM 목록 조회 |
| POST | `/api/v1/vms` | VM 생성 |
| GET | `/api/v1/vms/{id}` | VM 상세 조회 |
| DELETE | `/api/v1/vms/{id}` | VM 삭제 |
| POST | `/api/v1/vms/{id}/start` | VM 시작 |
| POST | `/api/v1/vms/{id}/stop` | VM 중지 |
| POST | `/api/v1/vms/{id}/pause` | VM 일시정지 |
| POST | `/api/v1/vms/{id}/resume` | VM 재개 |

### Storage (스토리지) - 4개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/storage/pools` | 스토리지 풀 목록 (ZFS/Ceph) |
| GET | `/api/v1/storage/volumes` | 볼륨 목록 (풀 필터 `?pool=`) |
| POST | `/api/v1/storage/volumes` | 볼륨 생성 |
| DELETE | `/api/v1/storage/volumes/{id}` | 볼륨 삭제 |

### Network (SDN) - 3개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/network/zones` | SDN 존 목록 (VXLAN/VLAN/Simple) |
| GET | `/api/v1/network/vnets` | 가상 네트워크 목록 (존 필터 `?zone=`) |
| GET | `/api/v1/network/firewall` | 방화벽 규칙 목록 |

### Peripheral (디바이스 패스스루) - 3개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/devices` | 디바이스 목록 (타입 필터 `?type=gpu`) |
| POST | `/api/v1/devices/{id}/attach` | VM에 디바이스 연결 |
| POST | `/api/v1/devices/{id}/detach` | 디바이스 분리 |

### HA / Cluster - 4개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/cluster/status` | 클러스터 상태 (quorum, leader, health) |
| GET | `/api/v1/cluster/nodes` | HA 노드 목록 |
| POST | `/api/v1/cluster/fence/{node}` | 펜싱 실행 |
| GET | `/api/v1/nodes` | 노드 리소스 현황 |

### Backup (백업) - 4개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/api/v1/backups` | 백업 목록 |
| POST | `/api/v1/backups` | 백업 생성 |
| GET | `/api/v1/backups/{id}` | 백업 상세 조회 |
| DELETE | `/api/v1/backups/{id}` | 백업 삭제 |

### Monitoring - 1개

| 메서드 | 경로 | 용도 |
|--------|------|------|
| GET | `/metrics` | Prometheus 메트릭 |

## curl 예제

### VM 생명주기

```bash
# VM 생성
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-vm","vcpus":2,"memory_mb":4096}' | jq

# QEMU 백엔드로 VM 생성 (Windows, GPU 패스스루 용도)
curl -s -X POST localhost:8080/api/v1/vms \
  -H 'Content-Type: application/json' \
  -d '{"name":"win-server","vcpus":8,"memory_mb":32768,"backend":"qemu"}' | jq

# VM 목록
curl -s localhost:8080/api/v1/vms | jq

# VM 상세
curl -s localhost:8080/api/v1/vms/1 | jq

# VM 시작
curl -s -X POST localhost:8080/api/v1/vms/1/start | jq

# VM 일시정지
curl -s -X POST localhost:8080/api/v1/vms/1/pause | jq

# VM 재개
curl -s -X POST localhost:8080/api/v1/vms/1/resume | jq

# VM 중지
curl -s -X POST localhost:8080/api/v1/vms/1/stop | jq

# VM 삭제
curl -s -X DELETE localhost:8080/api/v1/vms/1 -w '%{http_code}\n'
```

### 스토리지

```bash
# 풀 목록
curl -s localhost:8080/api/v1/storage/pools | jq

# 볼륨 생성
curl -s -X POST localhost:8080/api/v1/storage/volumes \
  -H 'Content-Type: application/json' \
  -d '{"pool":"local-zfs","name":"disk-01","size_bytes":10737418240,"format":"qcow2"}' | jq
```

### 디바이스 패스스루

```bash
# GPU 디바이스 목록
curl -s 'localhost:8080/api/v1/devices?type=gpu' | jq

# 디바이스 연결
curl -s -X POST localhost:8080/api/v1/devices/gpu-0/attach \
  -H 'Content-Type: application/json' \
  -d '{"vm_handle":1}' | jq
```

### 클러스터

```bash
# 클러스터 상태
curl -s localhost:8080/api/v1/cluster/status | jq

# 펜싱
curl -s -X POST localhost:8080/api/v1/cluster/fence/node-03 \
  -H 'Content-Type: application/json' \
  -d '{"reason":"unresponsive","action":"reboot"}' | jq
```

## 인증

RBAC가 활성화된 경우 Basic Auth를 사용한다:

```bash
# Basic Auth
curl -s -u admin:secret123 localhost:8080/api/v1/vms | jq

# 인증 없이 접근 가능한 엔드포인트
curl -s localhost:8080/healthz | jq
curl -s localhost:8080/metrics
```

## HTTP 상태 코드

| 코드 | 의미 |
|------|------|
| 200 | 성공 |
| 201 | 리소스 생성 성공 |
| 204 | 삭제 성공 (본문 없음) |
| 400 | 잘못된 요청 |
| 401 | 인증 실패 |
| 403 | 권한 부족 |
| 404 | 리소스 없음 |
| 409 | 잘못된 상태 전이 (예: 정지된 VM을 일시정지 시도) |
| 500 | 서버 내부 에러 |
