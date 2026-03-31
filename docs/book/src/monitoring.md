# 모니터링

## Prometheus 메트릭

Controller는 `/metrics` 엔드포인트에서 Prometheus 메트릭을 노출한다.

### 메트릭 목록

| 메트릭 | 타입 | 설명 |
|--------|------|------|
| `hcv_vms_total` | Gauge | 전체 VM 수 |
| `hcv_api_requests_total` | Counter | API 요청 총 수 |
| `hcv_api_request_duration_seconds` | Histogram | API 요청 처리 시간 |
| `hcv_nodes_total` | Gauge | 전체 노드 수 |
| `hcv_storage_pool_bytes` | Gauge | 스토리지 풀 사용량 (바이트) |

### 메트릭 수집 확인

```bash
# Prometheus 메트릭 확인
curl -s localhost:18080/metrics

# 특정 메트릭 필터링
curl -s localhost:18080/metrics | grep hcv_vms_total
```

## Grafana 대시보드

Docker 스택에 포함된 Grafana가 자동으로 프로비저닝된다.

### 접속

```bash
# Docker 스택 시작
just dev-up

# Grafana 접속
# URL: http://localhost:3000
# 기본 계정: admin / admin
```

### 대시보드 패널 (5개)

| 패널 | 설명 |
|------|------|
| **VM 상태** | VM 수와 상태별 분포 |
| **API 요청률** | 초당 API 요청 수 |
| **API 레이턴시** | API 응답 시간 분포 |
| **노드 수** | 클러스터 노드 현황 |
| **스토리지 사용량** | 스토리지 풀별 사용량 |

### 프로비저닝 파일

대시보드와 데이터소스는 Docker 스택 시작 시 자동으로 프로비저닝된다:

```
deploy/grafana/
  provisioning/
    dashboards/dashboard.yml      # 대시보드 프로비저닝 설정
    datasources/datasource.yml    # Prometheus 데이터소스 설정
  dashboards/
    hardcorevisor.json            # 5패널 대시보드 정의
```

## Prometheus 설정

### 수집 설정

```
deploy/prometheus.yml             # Prometheus 수집 설정 + rule_files
```

Controller의 `/metrics` 엔드포인트를 스크레이핑하도록 설정되어 있다.

### Alerting Rules

```
deploy/alert-rules.yml            # Prometheus 알람 규칙
```

| 알람 규칙 | 조건 | 대기 시간 |
|-----------|------|-----------|
| **NodeDown** | 노드 다운 | 1분 |
| **StorageHigh** | 스토리지 사용률 80%+ | 5분 |
| **APIErrorRate** | 5xx 에러 발생 | 2분 |
| **NoVMsRunning** | 실행 중인 VM 없음 | 5분 |

## Alertmanager

Prometheus 알람이 트리거되면 Alertmanager를 통해 알림을 전달할 수 있다. `deploy/alert-rules.yml`에 정의된 규칙이 Prometheus에서 평가되며, 조건이 충족되면 알람이 발생한다.

### Alertmanager 연동

Prometheus의 `alertmanagers` 설정에 Alertmanager 주소를 추가하면 알람 발생 시 Slack, PagerDuty, 웹훅 등으로 알림을 전달할 수 있다. Controller는 `/api/v1/webhooks/alert` 엔드포인트로 Alertmanager 웹훅을 수신할 수 있다.

### 대시보드 자동 프로비저닝

`just dev-up`으로 Docker 스택을 시작하면 Grafana 대시보드가 자동으로 프로비저닝된다. 대시보드 JSON은 `deploy/grafana/dashboards/hardcorevisor.json`에 정의되어 있으며, Phase 5-9에서 Backup Count, API Error Rate, Node Heartbeat, Request Duration P99 패널이 추가되었다.

### 알람 확인

```bash
# Prometheus 알람 상태 확인 (Prometheus UI)
# URL: http://localhost:9090/alerts

# API로 확인
curl -s localhost:9090/api/v1/alerts | jq
```

## Docker 스택 구성

```bash
# 전체 스택 시작
just dev-up

# 포트 매핑
#   etcd:       2379
#   Prometheus: 9090
#   Grafana:    3000
#   Controller: 18080

# 스택 로그 확인
just dev-logs

# 스택 중지
just dev-down
```
