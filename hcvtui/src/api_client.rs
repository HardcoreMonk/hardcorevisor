//! REST API client for communicating with Go Controller

use serde::{Deserialize, Serialize};

/// Base URL for the Go Controller REST API
const DEFAULT_BASE_URL: &str = "http://localhost:8080/api/v1";

/// API client configuration
pub struct ApiClient {
    base_url: String,
    client: reqwest::Client,
}

/// VM info returned from the API
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
}

/// Cluster node info
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct NodeInfo {
    pub name: String,
    pub status: String,
    pub cpu_percent: f64,
    pub memory_percent: f64,
    pub vm_count: u32,
}

/// Version info
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

/// API error type
#[derive(Debug)]
pub enum ApiError {
    Network(reqwest::Error),
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
    pub fn new(base_url: Option<String>) -> Self {
        Self {
            base_url: base_url.unwrap_or_else(|| DEFAULT_BASE_URL.to_string()),
            client: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(3))
                .build()
                .unwrap_or_default(),
        }
    }

    /// Get version info
    pub async fn version(&self) -> Result<VersionInfo, ApiError> {
        Ok(self
            .client
            .get(format!("{}/version", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// List all VMs
    pub async fn list_vms(&self) -> Result<Vec<VmInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/vms", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// List cluster nodes
    pub async fn list_nodes(&self) -> Result<Vec<NodeInfo>, ApiError> {
        Ok(self
            .client
            .get(format!("{}/nodes", self.base_url))
            .send()
            .await?
            .json()
            .await?)
    }

    /// Perform a VM lifecycle action (start/stop/pause/resume)
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

    /// Delete a VM
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

    /// Create a VM
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
}
