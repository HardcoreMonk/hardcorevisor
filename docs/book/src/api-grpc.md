# gRPC API

기본 주소: `localhost:19090`

## gRPC 서비스

HardCoreVisor는 3개의 gRPC 서비스를 제공하며, 총 17개 RPC를 포함한다.

### ComputeService (9 RPCs)

Proto 패키지: `hardcorevisor.compute.v1`

VM 생명주기 관리를 담당한다.

| RPC | 설명 |
|-----|------|
| `ListVMs` | VM 목록 조회 |
| `GetVM` | VM 상세 조회 |
| `CreateVM` | VM 생성 |
| `DeleteVM` | VM 삭제 |
| `StartVM` | VM 시작 |
| `StopVM` | VM 중지 |
| `PauseVM` | VM 일시정지 |
| `ResumeVM` | VM 재개 |
| `ListBackends` | VMM 백엔드 목록 |

### StorageAgent (5 RPCs)

Proto 패키지: `hardcorevisor.storage.v1`

스토리지 풀과 볼륨을 관리한다.

| RPC | 설명 |
|-----|------|
| `ListPools` | 스토리지 풀 목록 |
| `ListVolumes` | 볼륨 목록 |
| `CreateVolume` | 볼륨 생성 |
| `DeleteVolume` | 볼륨 삭제 |
| `CreateSnapshot` | 스냅샷 생성 |

### PeripheralManager (3 RPCs)

Proto 패키지: `hardcorevisor.peripheral.v1`

디바이스 패스스루를 관리한다.

| RPC | 설명 |
|-----|------|
| `ListDevices` | 디바이스 목록 |
| `AttachDevice` | VM에 디바이스 연결 |
| `DetachDevice` | 디바이스 분리 |

## grpcurl 예제

### Reflection으로 서비스 탐색

gRPC reflection이 활성화되어 있어 `grpcurl`로 서비스를 탐색할 수 있다.

```bash
# 서비스 목록
grpcurl -plaintext localhost:19090 list

# 서비스별 RPC 목록
grpcurl -plaintext localhost:19090 list hardcorevisor.compute.v1.ComputeService
grpcurl -plaintext localhost:19090 list hardcorevisor.storage.v1.StorageAgent
grpcurl -plaintext localhost:19090 list hardcorevisor.peripheral.v1.PeripheralManager

# RPC 상세 (요청/응답 메시지 타입)
grpcurl -plaintext localhost:19090 describe hardcorevisor.compute.v1.ComputeService.CreateVM
```

### Compute 예제

```bash
# VM 목록
grpcurl -plaintext localhost:19090 \
  hardcorevisor.compute.v1.ComputeService/ListVMs

# VM 생성
grpcurl -plaintext -d '{"name":"grpc-vm","vcpus":2,"memory_mb":4096}' \
  localhost:19090 hardcorevisor.compute.v1.ComputeService/CreateVM

# VM 시작
grpcurl -plaintext -d '{"handle":1}' \
  localhost:19090 hardcorevisor.compute.v1.ComputeService/StartVM

# VM 중지
grpcurl -plaintext -d '{"handle":1}' \
  localhost:19090 hardcorevisor.compute.v1.ComputeService/StopVM
```

### Storage 예제

```bash
# 풀 목록
grpcurl -plaintext localhost:19090 \
  hardcorevisor.storage.v1.StorageAgent/ListPools

# 볼륨 목록
grpcurl -plaintext localhost:19090 \
  hardcorevisor.storage.v1.StorageAgent/ListVolumes

# 볼륨 생성
grpcurl -plaintext -d '{"pool":"local-zfs","name":"disk-01","size_bytes":10737418240}' \
  localhost:19090 hardcorevisor.storage.v1.StorageAgent/CreateVolume
```

### Peripheral 예제

```bash
# 디바이스 목록
grpcurl -plaintext localhost:19090 \
  hardcorevisor.peripheral.v1.PeripheralManager/ListDevices

# 디바이스 연결
grpcurl -plaintext -d '{"device_id":"gpu-0","vm_handle":1}' \
  localhost:19090 hardcorevisor.peripheral.v1.PeripheralManager/AttachDevice
```

## Proto 소스

Proto 정의 파일은 `proto/` 디렉터리에 위치한다. 생성된 Go 코드는 `controller/pkg/proto/`에 있다.

```bash
# Proto 코드 재생성
just proto-gen
```

| Proto 파일 | 생성 패키지 |
|-----------|-----------|
| `proto/compute.proto` | `controller/pkg/proto/computepb/` |
| `proto/storage.proto` | `controller/pkg/proto/storagepb/` |
| `proto/peripheral.proto` | `controller/pkg/proto/peripheralpb/` |
