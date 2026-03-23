//! App 상태와 메인 이벤트 루프 (즉시 모드 렌더링 / Immediate Mode Rendering)
//!
//! ## 즉시 모드 렌더링 패턴
//!
//! 전통적인 GUI는 "상태가 바뀌면 해당 부분만 다시 그리는" 보존 모드(Retained Mode)를
//! 사용하지만, Ratatui TUI는 **즉시 모드**를 사용한다:
//!
//! ```text
//! ┌─────────────────────────────────────────┐
//! │  루프 시작                               │
//! │  ├─ 1. render(): 전체 UI를 처음부터 그림 │
//! │  ├─ 2. handle_input(): 키 입력 처리      │
//! │  └─ 3. tick(): API 폴링으로 데이터 갱신  │
//! │  다시 루프 시작 ...                      │
//! └─────────────────────────────────────────┘
//! ```
//!
//! Ratatui의 더블 버퍼링이 이전 프레임과의 diff만 터미널에 출력하므로
//! 매 프레임 전체를 그려도 깜빡임이 없다.
//!
//! ## 키바인딩 → 액션 → 상태 변경 흐름
//!
//! 1. `crossterm::event::read()`로 키 이벤트 수신
//! 2. `Action::from_key()`가 KeyCode + 현재 Screen을 보고 Action 결정
//! 3. `handle_key()`가 Action에 따라 App 상태 변경 (화면 전환, VM 제어 등)
//! 4. 다음 render()에서 변경된 상태가 UI에 반영됨
//!
//! ## tick() 폴링 흐름
//!
//! `poll_interval`(기본 2초)마다 Controller REST API를 호출하여
//! VM, 노드, 스토리지, 네트워크, HA 데이터를 갱신한다.
//! `tokio::join!`으로 모든 API 호출을 병렬 실행하여 대기 시간을 최소화한다.

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

/// 현재 활성화된 화면을 나타내는 열거형
///
/// 숫자 키 1~6으로 전환한다. 각 화면은 `ui/` 모듈의 대응하는 `render()` 함수가 그린다.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Screen {
    /// 대시보드: 클러스터 노드, VM 요약, 시스템 정보, 연결 상태를 2x2 그리드로 표시
    Dashboard,
    /// VM 매니저: VM 테이블 + 생명주기 제어 (시작/중지/일시정지/삭제/생성)
    VmManager,
    /// 스토리지 뷰: 스토리지 풀 + 볼륨 목록을 좌우 2열로 표시
    StorageView,
    /// 네트워크 뷰: SDN 존, 가상 네트워크, 방화벽 규칙을 수직 3단으로 표시
    NetworkView,
    /// 로그 뷰어: 이벤트 로그를 자동 스크롤로 표시 (최대 500개)
    LogViewer,
    /// HA 모니터: 클러스터 상태 + 노드 목록을 좌우 2열로 표시
    HaMonitor,
}

/// Controller API 연결 상태
///
/// tick()의 API 호출 결과에 따라 자동으로 전환된다.
/// 타이틀 바에 색상 인디케이터로 표시된다.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnStatus {
    /// 초기 상태 — 아직 API 응답을 받지 못함
    Disconnected,
    /// API 응답 정상 수신 중
    Connected,
    /// API 호출 에러 발생
    Error,
}

/// VM 생성 폼의 입력 상태
///
/// VM Manager 화면에서 'c' 키를 누르면 팝업으로 표시된다.
/// Tab/Shift+Tab으로 필드 간 이동, Enter로 제출, Esc로 취소한다.
pub struct CreateFormState {
    /// VM 이름 (필수, 빈 문자열이면 에러)
    pub name: String,
    /// 가상 CPU 수 (기본값: "2")
    pub vcpus: String,
    /// 메모리 크기 MB (기본값: "4096")
    pub memory_mb: String,
    /// VMM 백엔드: "rustvmm", "qemu", 또는 "lxc" (기본값: "rustvmm")
    ///
    /// type이 "container"이면 백엔드 값에 관계없이 lxc가 자동 선택된다.
    pub backend: String,
    /// 워크로드 타입: "vm" 또는 "container" (기본값: "vm", Phase 16 추가)
    ///
    /// "container"를 선택하면 create_container() API를 호출하여
    /// LXC 백엔드로 컨테이너를 생성한다.
    /// "vm"이면 기존 create_vm() API를 호출한다.
    pub workload_type: String,
    /// 현재 포커스된 필드 인덱스 (0=name, 1=vcpus, 2=memory, 3=backend, 4=type)
    pub focused_field: usize,
    /// 유효성 검사 또는 API 에러 메시지
    pub error: Option<String>,
}

/// 폼 필드 수 (Phase 16에서 4→5로 변경: name, vcpus, memory, backend, type)
///
/// type 필드가 추가되어 VM과 컨테이너 생성을 동일 폼에서 처리할 수 있다.
/// Tab/Shift+Tab으로 필드 간 이동 시 이 상수로 모듈러 연산한다.
const FORM_FIELD_COUNT: usize = 5;

impl CreateFormState {
    /// 기본값으로 새 폼 상태를 생성한다.
    pub fn new() -> Self {
        Self {
            name: String::new(),
            vcpus: "2".to_string(),
            memory_mb: "4096".to_string(),
            backend: "rustvmm".to_string(),
            workload_type: "vm".to_string(),
            focused_field: 0,
            error: None,
        }
    }
}

/// 메인 애플리케이션 상태
///
/// 모든 화면이 공유하는 단일 상태 구조체이다.
/// `run()` 메서드가 즉시 모드 이벤트 루프를 실행하며,
/// `tick()` 메서드가 주기적으로 Controller API를 폴링하여 데이터를 갱신한다.
pub struct App {
    /// false가 되면 이벤트 루프 종료 ('q' 키로 설정)
    pub running: bool,
    /// 현재 활성 화면
    pub screen: Screen,
    /// 이벤트 폴링 타임아웃 (기본 100ms — 입력 응답성과 CPU 사용률의 균형)
    pub tick_rate: Duration,

    // ── API 상태 ──
    /// REST API 클라이언트 (reqwest 기반, 3초 타임아웃)
    pub client: ApiClient,
    /// 현재 API 연결 상태
    pub conn_status: ConnStatus,
    /// 마지막 API 에러 메시지 (Status 패널에 표시)
    pub last_error: Option<String>,

    // ── Controller에서 수신한 데이터 ──
    /// VM 목록 (tick()마다 갱신)
    pub vms: Vec<VmInfo>,
    /// 클러스터 노드 목록 (Dashboard용)
    pub nodes: Vec<NodeInfo>,
    /// Controller 버전 정보 (최초 연결 시 1회 로드)
    pub version: VersionInfo,

    // ── 스토리지 데이터 ──
    /// 스토리지 풀 목록 (ZFS/Ceph)
    pub pools: Vec<PoolInfo>,
    /// 스토리지 볼륨 목록
    pub volumes: Vec<VolumeInfo>,

    // ── 네트워크 데이터 ──
    /// SDN 존 목록
    pub zones: Vec<ZoneInfo>,
    /// 가상 네트워크 목록
    pub vnets: Vec<VNetInfo>,

    // ── HA/클러스터 데이터 ──
    /// 클러스터 전체 상태 (쿼럼, 리더, 헬스)
    pub cluster: Option<ClusterStatusInfo>,
    /// HA 클러스터 노드 목록
    pub cluster_nodes: Vec<ClusterNodeInfo>,

    // ── 이벤트 로그 (순환 버퍼, 최대 500개) ──
    /// VM 상태 변경, 연결 이벤트 등을 기록하는 로그 엔트리
    pub log_entries: Vec<String>,

    // ── VM Manager 선택 상태 ──
    /// 현재 선택된 VM의 인덱스 (j/k 키로 이동)
    pub vm_selected: usize,

    // ── VM 상세 뷰 팝업 ──
    /// true면 선택된 VM의 상세 정보 팝업을 표시
    pub show_vm_detail: bool,

    // ── VM 생성 폼 팝업 ──
    /// true면 VM 생성 폼 팝업을 표시
    pub show_create_form: bool,
    /// 생성 폼의 현재 입력 상태
    pub create_form: CreateFormState,

    // ── 로그 뷰어 스크롤 ──
    /// 로그 뷰어의 수직 스크롤 오프셋 (현재 자동 스크롤 사용)
    #[allow(dead_code)]
    pub log_scroll: u16,

    // ── WebSocket 상태 ──
    /// Controller의 /ws 엔드포인트 사용 가능 여부
    pub ws_available: bool,
    /// WebSocket 가용성 확인 완료 플래그 (1회만 확인)
    ws_check_done: bool,

    // ── API 폴링 타이머 ──
    /// 마지막 API 폴링 시각
    last_poll: Instant,
    /// API 폴링 주기 (기본 2초)
    poll_interval: Duration,
}

impl App {
    /// 새 App 인스턴스를 생성한다.
    ///
    /// `HCV_API_ADDR` 환경변수가 설정되어 있으면 해당 주소의 Controller에 연결한다.
    /// 미설정 시 기본값 `http://localhost:8080/api/v1`을 사용한다.
    ///
    /// # 예시
    /// ```bash
    /// HCV_API_ADDR=192.168.1.100:8080 cargo run -p hcvtui
    /// ```
    pub fn new() -> Self {
        // 환경변수에서 API 주소를 읽어온다 (예: "192.168.1.100:8080")
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
            ws_available: false,
            ws_check_done: false,
            last_poll: Instant::now() - Duration::from_secs(10), // force immediate first poll
            poll_interval: Duration::from_secs(2),
        }
    }

    /// 메인 이벤트 루프 — 즉시 모드 렌더링 (Immediate Mode Rendering)
    ///
    /// 3단계 루프를 `running`이 false가 될 때까지 반복한다:
    ///
    /// 1. **렌더링**: 현재 App 상태를 기반으로 전체 UI를 그린다.
    ///    Ratatui의 더블 버퍼링이 변경된 셀만 터미널에 출력한다.
    /// 2. **입력 처리**: `tick_rate`(100ms) 동안 키 입력을 대기한다.
    ///    입력이 있으면 `handle_key()`로 처리한다.
    /// 3. **API 폴링**: `poll_interval`(2초)이 경과했으면 `tick()`으로
    ///    Controller API를 호출하여 데이터를 갱신한다.
    ///
    /// # 매개변수
    /// - `terminal`: Ratatui 터미널 인스턴스 (프레임 렌더링용)
    ///
    /// # 반환값
    /// - `Ok(())`: 정상 종료 ('q' 키로 종료)
    /// - `Err(...)`: 터미널 I/O 에러
    pub async fn run(&mut self, terminal: &mut DefaultTerminal) -> Result<()> {
        while self.running {
            // 1단계: 현재 프레임 렌더링 (즉시 모드 — 매번 전체를 다시 그림)
            terminal.draw(|frame| self.render(frame))?;

            // 2단계: 입력 이벤트 처리 (tick_rate 동안 논블로킹 대기)
            if event::poll(self.tick_rate)? {
                if let Event::Key(key) = event::read()? {
                    if key.kind == KeyEventKind::Press {
                        self.handle_key(key).await;
                    }
                }
            }

            // 3단계: 주기적 API 폴링 (poll_interval마다 실행)
            if self.last_poll.elapsed() >= self.poll_interval {
                self.tick().await;
                self.last_poll = Instant::now();
            }
        }
        Ok(())
    }

    /// Controller REST API를 폴링하여 최신 데이터를 가져온다.
    ///
    /// `tokio::join!`으로 8개 API 호출을 동시에 실행하여 대기 시간을 최소화한다.
    /// VM 상태 변경, VM 추가/삭제를 감지하여 이벤트 로그에 기록한다.
    /// 버전 정보와 WebSocket 가용성은 최초 연결 시 1회만 확인한다.
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

        // Check WebSocket availability once after connection is established
        if !self.ws_check_done && self.conn_status == ConnStatus::Connected {
            self.ws_available = self.client.check_ws().await;
            self.ws_check_done = true;
            if self.ws_available {
                self.push_log("[INFO] WebSocket endpoint available (WS Ready)".to_string());
            }
        }

        // Clamp selection index
        if !self.vms.is_empty() && self.vm_selected >= self.vms.len() {
            self.vm_selected = self.vms.len() - 1;
        }
    }

    /// 로그 엔트리를 추가한다. 최대 500개를 유지하며 초과 시 가장 오래된 항목을 제거한다.
    ///
    /// # 매개변수
    /// - `msg`: 로그 메시지 (UNIX 타임스탬프가 자동 추가됨)
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

    /// 현재 활성 화면을 렌더링한다.
    ///
    /// `self.screen` 값에 따라 해당 UI 모듈의 `render()` 함수를 호출한다.
    /// 즉시 모드이므로 매 프레임 전체를 처음부터 그린다.
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

    /// 키 입력 이벤트를 처리한다.
    ///
    /// 입력 처리 우선순위:
    /// 1. VM 생성 폼이 열려 있으면 → 폼 전용 입력 처리 (`handle_form_key`)
    /// 2. VM 상세 팝업이 열려 있으면 → Enter/Esc만 처리 (닫기)
    /// 3. 그 외 → 글로벌/화면별 키바인딩 처리 (`Action::from_key`)
    ///
    /// # 매개변수
    /// - `key_event`: crossterm에서 수신한 키 이벤트
    async fn handle_key(&mut self, key_event: KeyEvent) {
        // VM 생성 폼이 열려 있으면 모든 입력을 폼에서 처리
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

    /// VM 생성 폼에서의 키 입력을 처리한다.
    ///
    /// - Esc: 폼 닫기
    /// - Tab/Down: 다음 필드로 이동
    /// - Shift+Tab/Up: 이전 필드로 이동
    /// - Enter: 폼 제출 (유효성 검사 후 API 호출)
    /// - Backspace: 현재 필드에서 마지막 문자 삭제
    /// - 문자 입력: 현재 필드에 문자 추가 (Ctrl/Alt 조합은 무시)
    async fn handle_form_key(&mut self, key_event: KeyEvent) {
        match key_event.code {
            KeyCode::Esc => {
                self.show_create_form = false;
            }
            KeyCode::Tab | KeyCode::Down => {
                self.create_form.focused_field =
                    (self.create_form.focused_field + 1) % FORM_FIELD_COUNT;
            }
            KeyCode::BackTab | KeyCode::Up => {
                self.create_form.focused_field =
                    (self.create_form.focused_field + FORM_FIELD_COUNT - 1) % FORM_FIELD_COUNT;
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

    /// 현재 포커스된 폼 필드의 가변 참조를 반환한다.
    ///
    /// # 반환값
    /// - `focused_field` 인덱스에 해당하는 필드의 `&mut String`
    ///   (0=name, 1=vcpus, 2=memory_mb, 3=backend)
    fn current_form_field_mut(&mut self) -> &mut String {
        match self.create_form.focused_field {
            0 => &mut self.create_form.name,
            1 => &mut self.create_form.vcpus,
            2 => &mut self.create_form.memory_mb,
            3 => &mut self.create_form.backend,
            4 => &mut self.create_form.workload_type,
            _ => &mut self.create_form.name,
        }
    }

    /// VM 생성 폼을 제출한다: 유효성 검사 후 Controller API를 호출한다.
    ///
    /// 유효성 검사 실패 시 `create_form.error`에 에러 메시지를 설정한다.
    /// API 호출 성공 시 `tick()`으로 VM 목록을 갱신하고 폼을 닫는다.
    ///
    /// # 에러 조건
    /// - 이름이 비어 있으면 에러
    /// - vCPUs가 양의 정수가 아니면 에러
    /// - Memory가 양의 정수가 아니면 에러
    /// - API 호출 실패 시 에러
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

        // Phase 16: workload_type에 따라 VM 또는 컨테이너 생성 API를 분기한다.
        // "container" → create_container() (type=container, 기본 템플릿 "ubuntu")
        // "vm"       → create_vm() (기존 VM 생성)
        let is_container = self.create_form.workload_type.trim() == "container";
        let result = if is_container {
            self.client
                .create_container(&name, vcpus, memory_mb, "ubuntu")
                .await
        } else {
            self.client.create_vm(&name, vcpus, memory_mb).await
        };

        match result {
            Ok(_) => {
                self.tick().await;
                self.show_create_form = false;
            }
            Err(e) => {
                self.create_form.error = Some(format!("Create failed: {e}"));
            }
        }
    }

    /// 현재 선택된 VM을 반환한다 (선택이 유효한 경우).
    ///
    /// # 반환값
    /// - `Some(&VmInfo)`: 선택된 VM이 존재하는 경우
    /// - `None`: VM 목록이 비어 있거나 인덱스가 범위를 초과한 경우
    #[allow(dead_code)]
    pub fn selected_vm(&self) -> Option<&VmInfo> {
        self.vms.get(self.vm_selected)
    }
}
