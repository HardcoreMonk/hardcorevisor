//! App state and main event loop (Immediate Mode rendering)

use color_eyre::Result;
use crossterm::event::{self, Event, KeyCode, KeyEventKind};
use ratatui::{DefaultTerminal, Frame};
use std::time::{Duration, Instant};

use crate::api_client::{
    ApiClient, ClusterNodeInfo, ClusterStatusInfo, NodeInfo, PoolInfo, VNetInfo, VersionInfo,
    VmInfo, VolumeInfo, ZoneInfo,
};
use crate::keybindings::Action;
use crate::ui;

/// Which screen is currently active
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Screen {
    Dashboard,
    VmManager,
    StorageView,
    NetworkView,
    LogViewer,
    HaMonitor,
}

/// Connection status to the Controller API
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnStatus {
    Disconnected,
    Connected,
    Error,
}

/// Main application state
pub struct App {
    pub running: bool,
    pub screen: Screen,
    pub tick_rate: Duration,

    // API state
    pub client: ApiClient,
    pub conn_status: ConnStatus,
    pub last_error: Option<String>,

    // Data from Controller
    pub vms: Vec<VmInfo>,
    pub nodes: Vec<NodeInfo>,
    pub version: VersionInfo,

    // Storage data
    pub pools: Vec<PoolInfo>,
    pub volumes: Vec<VolumeInfo>,

    // Network data
    pub zones: Vec<ZoneInfo>,
    pub vnets: Vec<VNetInfo>,

    // HA/Cluster data
    pub cluster: Option<ClusterStatusInfo>,
    pub cluster_nodes: Vec<ClusterNodeInfo>,

    // Log entries (circular buffer for recent events)
    pub log_entries: Vec<String>,

    // VM Manager selection
    pub vm_selected: usize,

    // Log viewer scroll offset
    #[allow(dead_code)]
    pub log_scroll: u16,

    // Polling
    last_poll: Instant,
    poll_interval: Duration,
}

impl App {
    pub fn new() -> Self {
        let api_addr = std::env::var("HCV_API_ADDR")
            .ok()
            .map(|addr| format!("http://{}/api/v1", addr.trim_start_matches("http://")));

        Self {
            running: true,
            screen: Screen::Dashboard,
            tick_rate: Duration::from_millis(100),
            client: ApiClient::new(api_addr),
            conn_status: ConnStatus::Disconnected,
            last_error: None,
            vms: Vec::new(),
            nodes: Vec::new(),
            version: VersionInfo::default(),
            pools: Vec::new(),
            volumes: Vec::new(),
            zones: Vec::new(),
            vnets: Vec::new(),
            cluster: None,
            cluster_nodes: Vec::new(),
            log_entries: vec!["[INFO] HardCoreVisor TUI started".to_string()],
            vm_selected: 0,
            log_scroll: 0,
            last_poll: Instant::now() - Duration::from_secs(10), // force immediate first poll
            poll_interval: Duration::from_secs(2),
        }
    }

    /// Main event loop — Immediate Mode rendering
    pub async fn run(&mut self, terminal: &mut DefaultTerminal) -> Result<()> {
        while self.running {
            // 1. Render current frame
            terminal.draw(|frame| self.render(frame))?;

            // 2. Handle input events (non-blocking with timeout)
            if event::poll(self.tick_rate)? {
                if let Event::Key(key) = event::read()? {
                    if key.kind == KeyEventKind::Press {
                        self.handle_key(key.code).await;
                    }
                }
            }

            // 3. Tick: poll API data on interval
            if self.last_poll.elapsed() >= self.poll_interval {
                self.tick().await;
                self.last_poll = Instant::now();
            }
        }
        Ok(())
    }

    /// Poll the Controller API for fresh data
    async fn tick(&mut self) {
        let old_vm_count = self.vms.len();
        let old_vm_states: Vec<(String, String)> = self
            .vms
            .iter()
            .map(|v| (v.name.clone(), v.state.clone()))
            .collect();

        // Fetch all data in parallel
        let (
            vms_result,
            nodes_result,
            pools_result,
            volumes_result,
            zones_result,
            vnets_result,
            cluster_result,
            cluster_nodes_result,
        ) = tokio::join!(
            self.client.list_vms(),
            self.client.list_nodes(),
            self.client.list_pools(),
            self.client.list_volumes(),
            self.client.list_zones(),
            self.client.list_vnets(),
            self.client.cluster_status(),
            self.client.cluster_nodes(),
        );

        match vms_result {
            Ok(vms) => {
                // Generate log entries for VM changes
                if vms.len() != old_vm_count {
                    self.push_log(format!(
                        "[INFO] VM count changed: {} -> {}",
                        old_vm_count,
                        vms.len()
                    ));
                }
                for vm in &vms {
                    if let Some((_, old_state)) =
                        old_vm_states.iter().find(|(name, _)| name == &vm.name)
                    {
                        if old_state != &vm.state {
                            self.push_log(format!(
                                "[EVENT] VM '{}' state: {} -> {}",
                                vm.name, old_state, vm.state
                            ));
                        }
                    }
                }
                // Detect new VMs
                for vm in &vms {
                    if !old_vm_states.iter().any(|(name, _)| name == &vm.name) {
                        self.push_log(format!("[EVENT] VM '{}' created ({})", vm.name, vm.state));
                    }
                }
                self.vms = vms;
                self.conn_status = ConnStatus::Connected;
                self.last_error = None;
            }
            Err(e) => {
                self.conn_status = ConnStatus::Error;
                self.last_error = Some(format!("{e}"));
            }
        }

        if let Ok(nodes) = nodes_result {
            self.nodes = nodes;
        }
        if let Ok(pools) = pools_result {
            self.pools = pools;
        }
        if let Ok(volumes) = volumes_result {
            self.volumes = volumes;
        }
        if let Ok(zones) = zones_result {
            self.zones = zones;
        }
        if let Ok(vnets) = vnets_result {
            self.vnets = vnets;
        }
        if let Ok(cluster) = cluster_result {
            self.cluster = Some(cluster);
        }
        if let Ok(cnodes) = cluster_nodes_result {
            self.cluster_nodes = cnodes;
        }

        // Fetch version once if not yet loaded
        if self.version.version.is_empty() {
            if let Ok(ver) = self.client.version().await {
                self.push_log(format!(
                    "[INFO] Connected to {} v{}",
                    ver.product, ver.version
                ));
                self.version = ver;
            }
        }

        // Clamp selection index
        if !self.vms.is_empty() && self.vm_selected >= self.vms.len() {
            self.vm_selected = self.vms.len() - 1;
        }
    }

    /// Push a log entry, keeping at most 500 entries
    fn push_log(&mut self, msg: String) {
        use std::time::SystemTime;
        let ts = SystemTime::now()
            .duration_since(SystemTime::UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        let entry = format!("[{ts}] {msg}");
        self.log_entries.push(entry);
        if self.log_entries.len() > 500 {
            self.log_entries.remove(0);
        }
    }

    /// Render the current screen
    fn render(&self, frame: &mut Frame) {
        match self.screen {
            Screen::Dashboard => ui::dashboard::render(frame, self),
            Screen::VmManager => ui::vm_manager::render(frame, self),
            Screen::StorageView => ui::storage_view::render(frame, self),
            Screen::NetworkView => ui::network_view::render(frame, self),
            Screen::LogViewer => ui::log_viewer::render(frame, self),
            Screen::HaMonitor => ui::ha_monitor::render(frame, self),
        }
    }

    /// Handle key press events
    async fn handle_key(&mut self, key: KeyCode) {
        let action = Action::from_key(key, self.screen);
        match action {
            Action::Quit => self.running = false,
            Action::SwitchScreen(s) => self.screen = s,
            Action::ScrollUp => {
                if self.vm_selected > 0 {
                    self.vm_selected -= 1;
                }
            }
            Action::ScrollDown => {
                if !self.vms.is_empty() && self.vm_selected < self.vms.len() - 1 {
                    self.vm_selected += 1;
                }
            }
            Action::VmStart => {
                if let Some(vm) = self.vms.get(self.vm_selected) {
                    let _ = self.client.vm_action(vm.id, "start").await;
                    self.tick().await;
                }
            }
            Action::VmStop => {
                if let Some(vm) = self.vms.get(self.vm_selected) {
                    let _ = self.client.vm_action(vm.id, "stop").await;
                    self.tick().await;
                }
            }
            Action::VmPause => {
                if let Some(vm) = self.vms.get(self.vm_selected) {
                    let _ = self.client.vm_action(vm.id, "pause").await;
                    self.tick().await;
                }
            }
            Action::VmDelete => {
                if let Some(vm) = self.vms.get(self.vm_selected) {
                    let _ = self.client.delete_vm(vm.id).await;
                    self.tick().await;
                }
            }
            Action::Refresh => {
                self.tick().await;
            }
            Action::None | Action::Select | Action::Back | Action::Search | Action::Command => {}
        }
    }

    /// Get the currently selected VM (if any)
    #[allow(dead_code)]
    pub fn selected_vm(&self) -> Option<&VmInfo> {
        self.vms.get(self.vm_selected)
    }
}
