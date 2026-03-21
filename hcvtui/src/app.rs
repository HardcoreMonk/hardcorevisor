//! App state and main event loop (Immediate Mode rendering)

use color_eyre::Result;
use crossterm::event::{self, Event, KeyCode, KeyEventKind};
use ratatui::{DefaultTerminal, Frame};
use std::time::{Duration, Instant};

use crate::api_client::{ApiClient, NodeInfo, VersionInfo, VmInfo};
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

    // VM Manager selection
    pub vm_selected: usize,

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
            vm_selected: 0,
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
        // Fetch VMs and nodes in parallel
        let (vms_result, nodes_result) =
            tokio::join!(self.client.list_vms(), self.client.list_nodes());

        match vms_result {
            Ok(vms) => {
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

        // Fetch version once if not yet loaded
        if self.version.version.is_empty() {
            if let Ok(ver) = self.client.version().await {
                self.version = ver;
            }
        }

        // Clamp selection index
        if !self.vms.is_empty() && self.vm_selected >= self.vms.len() {
            self.vm_selected = self.vms.len() - 1;
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
