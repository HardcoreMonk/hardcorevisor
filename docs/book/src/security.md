# 보안

## RBAC 역할

HardCoreVisor는 3단계 역할 기반 접근 제어(RBAC)를 지원한다.

### 역할 정의

| 역할 | 읽기 (GET) | 쓰기 (POST/PUT/DELETE) | 관리 |
|------|-----------|----------------------|------|
| `admin` | O | O | O |
| `operator` | O | O | X |
| `viewer` | O | X | X |

### 사용자 설정

`HCV_RBAC_USERS` 환경변수로 사용자를 정의한다:

```bash
# 형식: user1:password1:role,user2:password2:role
export HCV_RBAC_USERS="admin:secret123:admin,devops:pass456:operator,monitor:view789:viewer"
```

또는 `hcv.yaml` 설정 파일:

```yaml
auth:
  users: "admin:secret123:admin,devops:pass456:operator,monitor:view789:viewer"
```

### 인증 제외 엔드포인트

다음 엔드포인트는 RBAC가 활성화되어도 인증 없이 접근 가능하다:

- `/healthz` - 헬스 체크
- `/metrics` - Prometheus 메트릭

### RBAC 비활성화

`HCV_RBAC_USERS`를 설정하지 않거나 비워두면 RBAC가 비활성화되어 모든 요청이 허용된다.

## 감사 로깅

모든 API 호출이 구조화 JSON으로 기록된다.

### 감사 로그 형식

```json
{
  "audit": true,
  "ts": "2025-01-15T10:30:00Z",
  "user": "admin",
  "method": "POST",
  "path": "/api/v1/vms",
  "status": 201,
  "duration_ms": 1.2
}
```

### 감사 로그 필드

| 필드 | 설명 |
|------|------|
| `audit` | 감사 로그 마커 (항상 `true`) |
| `ts` | 타임스탬프 (ISO 8601) |
| `user` | 인증된 사용자명 (RBAC 비활성 시 빈 문자열) |
| `method` | HTTP 메서드 |
| `path` | 요청 경로 |
| `status` | HTTP 상태 코드 |
| `duration_ms` | 처리 시간 (밀리초) |

### JSON 포맷 로그 활성화

프로덕션 환경에서는 JSON 포맷 로깅을 권장한다:

```bash
HCV_LOG_FORMAT=json just go-run
```

## TLS

HTTPS/gRPC-TLS를 활성화하여 전송 계층을 암호화할 수 있다.

### TLS 설정

```bash
# 환경변수로 설정
export HCV_TLS_CERT=/path/to/cert.pem
export HCV_TLS_KEY=/path/to/key.pem
just go-run
```

또는 `hcv.yaml`:

```yaml
tls:
  cert_file: "/path/to/cert.pem"
  key_file: "/path/to/key.pem"
```

### 자체 서명 인증서로 테스트

```bash
# 자체 서명 인증서 생성
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes

# TLS로 Controller 시작
HCV_TLS_CERT=cert.pem HCV_TLS_KEY=key.pem just go-run

# curl로 접속 (자체 서명 인증서이므로 -k 플래그 필요)
curl -sk https://localhost:8080/healthz | jq

# hcvctl로 접속
hcvctl --tls-skip-verify vm list
```

## Basic Auth

RBAC 활성화 시 Basic Auth로 인증한다.

```bash
# curl
curl -s -u admin:secret123 localhost:8080/api/v1/vms | jq

# hcvctl
hcvctl --user admin --password secret123 vm list
```

## 환경변수 보안

### 민감한 환경변수

| 환경변수 | 내용 | 보안 권장사항 |
|----------|------|-------------|
| `HCV_RBAC_USERS` | 사용자 이름, 비밀번호, 역할 | 프로덕션에서는 시크릿 관리 시스템 사용 |
| `HCV_TLS_CERT` | TLS 인증서 경로 | 적절한 파일 권한 설정 (644) |
| `HCV_TLS_KEY` | TLS 개인 키 경로 | 엄격한 파일 권한 설정 (600) |
| `HCV_ETCD_ENDPOINTS` | etcd 연결 정보 | 네트워크 격리 또는 TLS 사용 |

### 보안 모범 사례

1. `.env` 파일에 민감한 정보를 저장하고, `.gitignore`에 포함되어 있는지 확인
2. 프로덕션 환경에서는 시크릿 관리 시스템 (HashiCorp Vault, Kubernetes Secrets 등) 사용
3. TLS 개인 키 파일 권한을 `600`으로 제한
4. RBAC 비밀번호는 충분한 복잡성 확보
5. 감사 로그를 중앙 로그 수집기로 전송하여 보관

## 미들웨어 체인

API 요청은 다음 미들웨어 체인을 통과한다:

```
RequestID → Audit → Logging → Metrics → RBAC → CORS → Recovery → Handler
```

| 미들웨어 | 기능 |
|----------|------|
| RequestID | `X-Request-Id` 헤더 자동 생성 |
| Audit | 감사 로그 기록 |
| Logging | 요청/응답 로깅 |
| Metrics | Prometheus 메트릭 수집 |
| RBAC | 역할 기반 접근 제어 |
| CORS | Cross-Origin 요청 허용 |
| Recovery | 패닉 복구 (서버 안정성) |
