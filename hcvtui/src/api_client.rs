//! Go Controller REST API 클라이언트
//!
//! reqwest HTTP 클라이언트를 사용하여 Controller의 REST API와 통신한다.
//! 모든 API 호출은 비동기(async)이며, 3초 타임아웃이 적용된다.
//!
//! ## 아키텍처 위치
//!
//! ```text
//! hcvtui (TUI)
//!     └── ApiClient (이 모듈)
//!             │ HTTP GET/POST/DELETE
//!             ▼
//!         Go Controller (:8080)
//!             └── /api/v1/vms, /api/v1/nodes, ...
//! ```
//!
//! ## 사용 패턴
//!
//! `app.rs`의 `tick()`이 2초마다 `list_vms()`, `list_nodes()` 등을
//! `tokio::join!`으로 병렬 호출하여 최신 데이터를 가져온다.
//! VM 제어 액션(`vm_action`, `create_vm`, `delete_vm`)은
//! 사용자 입력 시 즉시 호출된다.

use serde::{Deserialize, Serialize};

/// Go Controller REST API 기본 주소
const DEFAULT_BASE_URL: &str = "http://localhost:8080/api/v1";

/// 스토리지 풀 정보
///
/// Controller의 `GET /api/v1/storage/pools` 응답에 매핑된다.
/// `#[serde(default)]`로 필드가 누락되어도 역직렬화가 실패하지 않는다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct PoolInfo {
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "type")]
    pub pool_type: String,
    #[serde(default)]
    pub total_bytes: u64,
    #[serde(default)]
    pub used_bytes: u64,
    #[serde(default)]
    pub health: String,
}

/// 스토리지 볼륨 정보
///
/// Controller의 `GET /api/v1/storage/volumes` 응답에 매핑된다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct VolumeInfo {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub pool: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub size_bytes: u64,
    #[serde(default)]
    pub format: String,
    #[serde(default)]
    pub path: String,
}

/// SDN 존 정보 (Software Defined Networking)
///
/// Controller의 `GET /api/v1/network/zones` 응답에 매핑된다.
/// 존 타입: VXLAN, VLAN, Simple 등
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ZoneInfo {
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "type")]
    pub zone_type: String,
    #[serde(default)]
    pub mtu: u32,
    #[serde(default)]
    pub bridge: String,
    #[serde(default)]
    pub status: String,
}

/// 가상 네트워크 정보
///
/// Controller의 `GET /api/v1/network/vnets` 응답에 매핑된다.
/// VLAN 태그와 서브넷 정보를 포함한다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct VNetInfo {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub zone: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub tag: u32,
    #[serde(default)]
    pub subnet: String,
    #[serde(default)]
    pub status: String,
}

/// 클러스터 전체 상태 정보
///
/// Controller의 `GET /api/v1/cluster/status` 응답에 매핑된다.
/// 쿼럼 유지 여부, 리더 노드, 전체 헬스 상태를 포함한다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ClusterStatusInfo {
    #[serde(default)]
    pub quorum: bool,
    #[serde(default)]
    pub node_count: u32,
    #[serde(default)]
    pub online_count: u32,
    #[serde(default)]
    pub leader: String,
    #[serde(default)]
    pub status: String,
}

/// HA 클러스터 노드 정보
///
/// Controller의 `GET /api/v1/cluster/nodes` 응답에 매핑된다.
/// 리더 여부, 펜스 에이전트, 관리 중인 VM 수를 포함한다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ClusterNodeInfo {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub last_seen: String,
    #[serde(default)]
    pub is_leader: bool,
    #[serde(default)]
    pub vm_count: u32,
    #[serde(default)]
    pub fence_agent: String,
}

/// REST API 클라이언트
///
/// reqwest 기반 HTTP 클라이언트로, 3초 타임아웃이 설정되어 있다.
/// Controller가 응답하지 않으면 타임아웃 에러가 `ApiError::Network`로 반환된다.
pub struct ApiClient {
    /// API 기본 URL (예: "http://localhost:8080/api/v1")
    base_url: String,
    /// reqwest HTTP 클라이언트 (커넥션 풀 자동 관리)
    client: reqwest::Client,
}

/// VM 정보 — Controller API에서 반환되는 VM 상태
///
/// Controller의 `GET /api/v1/vms` 응답의 각 항목에 매핑된다.
/// `state` 필드: "configured", "running", "paused", "stopped"
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct VmInfo {
    pub id: i32,
    pub name: String,
    pub state: String,
    #[serde(default)]
    pub vcpus: u32,
    #[serde(default)]
    pub memory_mb: u64,
    #[serde(default)]
    pub node: String,
    #[serde(default)]
    pub backend: String,
    /// 워크로드 타입: "vm" 또는 "container" (Phase 16 추가)
    ///
    /// VM Manager 화면에서 TYPE 컬럼으로 표시된다.
    /// "container"이면 LXC 백엔드로 관리되는 컨테이너이다.
    /// 하위 호환성을 위해 기본값은 "vm" (default_vm_type 함수).
    #[serde(default = "default_vm_type", rename = "type")]
    pub vm_type: String,
    /// LXC 배포 템플릿 이름 (Phase 16 추가)
    ///
    /// 컨테이너 생성 시 사용된 템플릿 (예: "ubuntu", "alpine", "debian").
    /// VM인 경우 빈 문자열이다.
    #[serde(default)]
    pub template: String,
}

fn default_vm_type() -> String {
    "vm".to_string()
}

/// 클러스터 노드 리소스 정보 (Dashboard용)
///
/// Controller의 `GET /api/v1/nodes` 응답에 매핑된다.
/// CPU/메모리 사용률과 관리 중인 VM 수를 포함한다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct NodeInfo {
    pub name: String,
    pub status: String,
    pub cpu_percent: f64,
    pub memory_percent: f64,
    pub vm_count: u32,
}

/// Controller 버전 정보
///
/// Controller의 `GET /api/v1/version` 응답에 매핑된다.
/// 제품명, 버전, 아키텍처, vmcore 버전을 포함한다.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct VersionInfo {
    #[serde(default)]
    pub product: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub arch: String,
    #[serde(default)]
    pub vmcore_version: String,
}

/// API 에러 타입
///
/// 두 가지 에러 유형을 구분한다:
/// - `Network`: 네트워크 수준 에러 (연결 실패, 타임아웃, DNS 에러 등)
/// - `Status`: HTTP 응답은 받았지만 상태 코드가 실패인 경우 (4xx, 5xx)
#[derive(Debug)]
pub enum ApiError {
    /// 네트워크 에러 (연결 실패, 타임아웃 등)
    Network(reqwest::Error),
    /// HTTP 에러 응답 (상태 코드, 응답 본문)
    Status(u16, String),
}

impl From<reqwest::Error> for ApiError {
    fn from(e: reqwest::Error) -> Self {
        ApiError::Network(e)
    }
}

impl std::fmt::Display for ApiError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ApiError::Network(e) => write!(f, "network: {e}"),
            ApiError::Status(code, msg) => write!(f, "HTTP {code}: {msg}"),
        }
    }
}

impl ApiClient {
    /// 새 API 클라이언트를 생성한다.
    ///
    /// # 매개변수
    /// - `base_url`: API 기본 URL. `None`이면 기본값 `http://localhost:8080/api/v1` 사용
    ///
    /// # 반환값
    /// - 3초 타임아웃이 설정된 `ApiClient` 인스턴스
    pub fn new(base_url: Option<String>) -> Self {
        Self {
            base_url: base_url.unwrap_or_else(|| DEFAULT_BASE_URL.to_string()),
            client: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(3))
                .build()
                .unwrap_or_default(),
        }
    }

    /// Controller 버전 정보를 조회한다 (`GET /api/v1/version`).
    ///
    /// # 반환값
    /// - `Ok(VersionInfo)`: 제품명, 버전, 아키텍처, vmcore 버전
    /// - `Err(ApiError)`: 네트워크 에러 또는 역직렬화 실패
    pub async fn version(&self) -> Result<VersionInfo, ApiError> {
        Ok(self
            .client
            .get(format!("{}/version", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// 모든 VM 목록을 조회한다 (`GET /api/v1/vms`).
    ///
    /// # 반환값
    /// - `Ok(Vec<VmInfo>)`: VM 목록 (ID, 이름, 상태, vCPU, 메모리, 노드, 백엔드)
    /// - `Err(ApiError)`: 네트워크 에러 또는 역직렬화 실패
    pub async fn list_vms(&self) -> Result<Vec<VmInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/vms", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// 클러스터 노드 리소스 현황을 조회한다 (`GET /api/v1/nodes`).
    pub async fn list_nodes(&self) -> Result<Vec<NodeInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/nodes", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// VM 생명주기 액션을 수행한다 (`POST /api/v1/vms/{id}/{action}`).
    ///
    /// # 매개변수
    /// - `id`: VM ID
    /// - `action`: "start", "stop", "pause", "resume" 중 하나
    ///
    /// # 반환값
    /// - `Ok(VmInfo)`: 액션 후 갱신된 VM 정보
    /// - `Err(ApiError::Status(409, _))`: 잘못된 상태 전이 (예: 이미 실행 중인 VM을 start)
    pub async fn vm_action(&self, id: i32, action: &str) -> Result<VmInfo, ApiError> {
        let resp = self
            .client
            .post(format!("{}/vms/{}/{}", self.base_url, id, action))
            .send()
            .await?;
        if !resp.status().is_success() {
            let code = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(ApiError::Status(code, body));
        }
        Ok(resp.json().await?)
    }

    /// VM을 삭제한다 (`DELETE /api/v1/vms/{id}`).
    ///
    /// # 매개변수
    /// - `id`: 삭제할 VM의 ID
    ///
    /// # 반환값
    /// - `Ok(())`: 성공 (HTTP 204)
    /// - `Err(ApiError::Status(404, _))`: VM을 찾을 수 없음
    pub async fn delete_vm(&self, id: i32) -> Result<(), ApiError> {
        let resp = self
            .client
            .delete(format!("{}/vms/{}", self.base_url, id))
            .send()
            .await?;
        if !resp.status().is_success() {
            let code = resp.status().as_u16();
            let body = resp.text().await.unwrap_or_default();
            return Err(ApiError::Status(code, body));
        }
        Ok(())
    }

    /// 스토리지 풀 목록을 조회한다 (`GET /api/v1/storage/pools`).
    #[allow(dead_code)]
    pub async fn list_pools(&self) -> Result<Vec<PoolInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/storage/pools", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// 스토리지 볼륨 목록을 조회한다 (`GET /api/v1/storage/volumes`).
    #[allow(dead_code)]
    pub async fn list_volumes(&self) -> Result<Vec<VolumeInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/storage/volumes", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// SDN 존 목록을 조회한다 (`GET /api/v1/network/zones`).
    #[allow(dead_code)]
    pub async fn list_zones(&self) -> Result<Vec<ZoneInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/network/zones", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// 가상 네트워크 목록을 조회한다 (`GET /api/v1/network/vnets`).
    #[allow(dead_code)]
    pub async fn list_vnets(&self) -> Result<Vec<VNetInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/network/vnets", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// 클러스터 상태를 조회한다 (`GET /api/v1/cluster/status`).
    #[allow(dead_code)]
    pub async fn cluster_status(&self) -> Result<ClusterStatusInfo, ApiError> {
        Ok(self
            .client
            .get(format!("{}/cluster/status", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// HA 클러스터 노드 목록을 조회한다 (`GET /api/v1/cluster/nodes`).
    #[allow(dead_code)]
    pub async fn cluster_nodes(&self) -> Result<Vec<ClusterNodeInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/cluster/nodes", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// WebSocket 엔드포인트 가용성을 확인한다 (`HEAD /ws`).
    ///
    /// Controller가 WebSocket을 지원하는지 1초 타임아웃으로 확인한다.
    /// 최초 연결 성공 시 1회만 호출된다.
    ///
    /// # 반환값
    /// - `true`: /ws 엔드포인트가 응답함
    /// - `false`: 연결 실패 또는 타임아웃
    pub async fn check_ws(&self) -> bool {
        let ws_url = self.base_url.replace("/api/v1", "/ws");
        self.client
            .head(&ws_url)
            .timeout(std::time::Duration::from_secs(1))
            .send()
            .await
            .is_ok()
    }

    /// 새 VM을 생성한다 (`POST /api/v1/vms`).
    ///
    /// # 매개변수
    /// - `name`: VM 이름
    /// - `vcpus`: 가상 CPU 수
    /// - `memory_mb`: 메모리 크기 (MB)
    ///
    /// # 반환값
    /// - `Ok(VmInfo)`: 생성된 VM 정보 (상태: "configured")
    /// - `Err(ApiError)`: 생성 실패
    #[allow(dead_code)]
    pub async fn create_vm(
        &self,
        name: &str,
        vcpus: u32,
        memory_mb: u64,
    ) -> Result<VmInfo, ApiError> {
        Ok(self
            .client
            .post(format!("{}/vms", self.base_url))
            .json(&serde_json::json!({
                "name": name,
                "vcpus": vcpus,
                "memory_mb": memory_mb,
            }))
            .send()
            .await?
            .json()
            .await?)
    }

    /// LXC 컨테이너를 생성한다 (`POST /api/v1/vms`, type=container).
    ///
    /// VM 생성 API와 동일한 엔드포인트를 사용하되, type="container"와
    /// template 필드를 추가하여 LXC 백엔드가 자동 선택되도록 한다.
    ///
    /// # 매개변수
    /// - `name`: 컨테이너 이름
    /// - `vcpus`: 가상 CPU 수 (cgroup2 cpu.max로 제한)
    /// - `memory_mb`: 메모리 크기 (MB) (cgroup2 memory.max로 제한)
    /// - `template`: LXC 배포 템플릿 (예: "ubuntu", "alpine")
    ///
    /// # 반환값
    /// - `Ok(VmInfo)`: 생성된 컨테이너 정보 (backend: "lxc", type: "container")
    /// - `Err(ApiError)`: 생성 실패
    pub async fn create_container(
        &self,
        name: &str,
        vcpus: u32,
        memory_mb: u64,
        template: &str,
    ) -> Result<VmInfo, ApiError> {
        Ok(self
            .client
            .post(format!("{}/vms", self.base_url))
            .json(&serde_json::json!({
                "name": name,
                "vcpus": vcpus,
                "memory_mb": memory_mb,
                "type": "container",
                "template": template,
            }))
            .send()
            .await?
            .json()
            .await?)
    }
}
