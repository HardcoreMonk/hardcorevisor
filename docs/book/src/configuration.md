# 설정

## hcv.yaml 설정 파일

Controller는 작업 디렉터리의 `hcv.yaml` 파일에서 설정을 로드한다. 환경변수가 항상 YAML 설정보다 우선한다.

```yaml
# HardCoreVisor Controller Configuration

# REST API 서버
api:
  addr: ":8080"          # HCV_API_ADDR

# gRPC 서버
grpc:
  addr: ":9090"          # HCV_GRPC_ADDR

# etcd 상태 저장소 (비워두면 인메모리 폴백)
etcd:
  endpoints: ""          # HCV_ETCD_ENDPOINTS (쉼표 구분, 예: "localhost:2379")

# TLS (생략하면 plain HTTP/gRPC)
tls:
  cert_file: ""          # HCV_TLS_CERT
  key_file: ""           # HCV_TLS_KEY

# RBAC 인증 (생략하거나 비워두면 비활성)
auth:
  users: ""              # HCV_RBAC_USERS (형식: "user1:pass1:admin,user2:pass2:viewer")

# 로깅
log:
  level: "info"          # HCV_LOG_LEVEL  (debug, info, warn, error)
  format: "text"         # HCV_LOG_FORMAT (text, json)
```

전체 예제는 프로젝트 루트의 `hcv.example.yaml`을 참고한다.

## 환경변수

| 환경변수 | 기본값 | 설명 |
|----------|--------|------|
| `HCV_API_ADDR` | `:8080` | REST API 서버 바인드 주소 |
| `HCV_GRPC_ADDR` | `:9090` | gRPC 서버 바인드 주소 |
| `HCV_ETCD_ENDPOINTS` | (없음) | etcd 엔드포인트 (쉼표 구분). 미설정 시 인메모리 폴백 |
| `HCV_TLS_CERT` | (없음) | TLS 인증서 파일 경로 |
| `HCV_TLS_KEY` | (없음) | TLS 키 파일 경로 |
| `HCV_RBAC_USERS` | (없음) | RBAC 사용자 정의 (`user:pass:role,...`) |
| `HCV_LOG_LEVEL` | `info` | 로그 레벨 (debug, info, warn, error) |
| `HCV_LOG_FORMAT` | `text` | 로그 포맷 (text, json) |

## 로그 레벨 / 포맷

Go `log/slog` 기반 구조화 로깅을 사용한다.

### 로그 레벨

| 레벨 | 용도 |
|------|------|
| `debug` | 개발/디버깅용 상세 로그 |
| `info` | 일반 운영 로그 (기본값) |
| `warn` | 경고 (비정상적이지만 동작 가능) |
| `error` | 에러 (즉시 조치 필요) |

### 로그 포맷

- **text**: 사람이 읽기 좋은 형식 (개발용)
- **json**: 구조화 JSON (프로덕션, 로그 수집기 연동)

```bash
# JSON 포맷으로 실행
HCV_LOG_FORMAT=json HCV_LOG_LEVEL=debug just go-run
```

## etcd 설정

etcd는 VM 상태 영속화에 사용된다. 미설정 시 인메모리 폴백으로 동작한다.

```bash
# Docker로 etcd 시작
just dev-up

# etcd 연결 설정
export HCV_ETCD_ENDPOINTS=localhost:2379
just go-run
```

`PersistentComputeService`가 `ComputeService`를 래핑하여 VM CRUD 시 자동으로 etcd에 저장한다. Controller 재시작 시 `LoadFromStore()`로 상태를 복원한다.

## RBAC 사용자 설정

`HCV_RBAC_USERS` 환경변수로 사용자를 정의한다:

```bash
# 형식: user1:password1:role,user2:password2:role
export HCV_RBAC_USERS="admin:secret123:admin,devops:pass456:operator,monitor:view789:viewer"
```

역할별 권한:

| 역할 | 읽기 | 쓰기 | 관리 |
|------|------|------|------|
| `admin` | O | O | O |
| `operator` | O | O | X |
| `viewer` | O | X | X |

`/healthz`와 `/metrics` 엔드포인트는 인증 없이 접근 가능하다.
