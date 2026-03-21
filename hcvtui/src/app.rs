//! App state and main event loop (Immediate Mode rendering)

use color_eyre::Result;
use crossterm::event::{self, Event, KeyCode, KeyEvent, KeyEventKind, KeyModifiers};
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

/// State for the VM creation form
pub struct CreateFormState {
    pub name: String,
    pub vcpus: String,
    pub memory_mb: String,
    pub backend: String,
    pub focused_field: usize,
    pub error: Option<String>,
}

impl CreateFormState {
    pub fn new() -> Self {
        Self {
            name: String::new(),
            vcpus: "2".to_string(),
            memory_mb: "4096".to_string(),
            backend: "rustvmm".to_string(),
            focused_field: 0,
            error: None,
        }
    }
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

    // VM detail view
    pub show_vm_detail: bool,

    // VM creation form
    pub show_create_form: bool,
    pub create_form: CreateFormState,

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
            show_vm_detail: false,
            show_create_form: false,
            create_form: CreateFormState::new(),
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
                        self.handle_key(key).await;
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
    async fn handle_key(&mut self, key_event: KeyEvent) {
        // When create form is open, capture all input for the form
        if self.show_create_form {
            self.handle_form_key(key_event).await;
            return;
        }

        // When VM detail is open, only handle close actions
        if self.show_vm_detail {
            let action = Action::from_key(key_event.code, self.screen);
            match action {
                Action::Select | Action::Back => {
                    self.show_vm_detail = false;
                }
                _ => {}
            }
            return;
        }

        let action = Action::from_key(key_event.code, self.screen);
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
            Action::CreateForm => {
                self.show_create_form = true;
                self.create_form = CreateFormState::new();
            }
            Action::Refresh => {
                self.tick().await;
            }
            Action::Select => {
                if self.screen == Screen::VmManager && !self.vms.is_empty() {
                    self.show_vm_detail = true;
                }
            }
            Action::None | Action::Back | Action::Search | Action::Command => {}
        }
    }

    /// Handle key input when the VM creation form is active
    async fn handle_form_key(&mut self, key_event: KeyEvent) {
        match key_event.code {
            KeyCode::Esc => {
                self.show_create_form = false;
            }
            KeyCode::Tab | KeyCode::Down => {
                self.create_form.focused_field = (self.create_form.focused_field + 1) % 4;
            }
            KeyCode::BackTab | KeyCode::Up => {
                self.create_form.focused_field = (self.create_form.focused_field + 3) % 4;
            }
            KeyCode::Enter => {
                self.submit_create_form().await;
            }
            KeyCode::Backspace => {
                let field = self.current_form_field_mut();
                field.pop();
            }
            KeyCode::Char(c) => {
                // Ignore ctrl/alt modified chars in form
                if key_event
                    .modifiers
                    .intersects(KeyModifiers::CONTROL | KeyModifiers::ALT)
                {
                    return;
                }
                let field = self.current_form_field_mut();
                field.push(c);
            }
            _ => {}
        }
    }

    /// Get a mutable reference to the currently focused form field
    fn current_form_field_mut(&mut self) -> &mut String {
        match self.create_form.focused_field {
            0 => &mut self.create_form.name,
            1 => &mut self.create_form.vcpus,
            2 => &mut self.create_form.memory_mb,
            3 => &mut self.create_form.backend,
            _ => &mut self.create_form.name,
        }
    }

    /// Submit the create form: validate and call API
    async fn submit_create_form(&mut self) {
        let name = self.create_form.name.trim().to_string();
        if name.is_empty() {
            self.create_form.error = Some("Name cannot be empty".to_string());
            return;
        }
        let vcpus: u32 = match self.create_form.vcpus.trim().parse() {
            Ok(v) if v > 0 => v,
            _ => {
                self.create_form.error = Some("vCPUs must be a positive number".to_string());
                return;
            }
        };
        let memory_mb: u64 = match self.create_form.memory_mb.trim().parse() {
            Ok(v) if v > 0 => v,
            _ => {
                self.create_form.error = Some("Memory must be a positive number".to_string());
                return;
            }
        };

        match self.client.create_vm(&name, vcpus, memory_mb).await {
            Ok(_) => {
                self.tick().await;
                self.show_create_form = false;
            }
            Err(e) => {
                self.create_form.error = Some(format!("Create failed: {e}"));
            }
        }
    }

    /// Get the currently selected VM (if any)
    #[allow(dead_code)]
    pub fn selected_vm(&self) -> Option<&VmInfo> {
        self.vms.get(self.vm_selected)
    }
}
