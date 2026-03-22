//! 키바인딩 정의 — vim 스타일 네비게이션
//!
//! ## 키바인딩 → 액션 → 상태 변경 흐름
//!
//! ```text
//! 키 입력 (crossterm)
//!     │
//!     ▼
//! Action::from_key(KeyCode, Screen)  ← 현재 화면에 따라 다른 액션 생성
//!     │
//!     ▼
//! App::handle_key(Action)            ← 액션에 따라 App 상태 변경
//!     │
//!     ▼
//! 다음 render()에서 UI 반영           ← 즉시 모드이므로 자동 반영
//! ```
//!
//! ## 키 매핑 규칙
//!
//! - **글로벌 키**: 모든 화면에서 동작 (q, 1-6, r, j/k, Enter, Esc)
//! - **화면별 키**: 특정 화면에서만 동작 (s/x/p/d/c → VM Manager 전용)
//! - VM 생성 폼이 열려 있으면 이 모듈이 아닌 `App::handle_form_key()`가 처리

use crate::app::Screen;
use crossterm::event::KeyCode;

/// 키 입력으로 트리거되는 액션
///
/// `App::handle_key()`가 이 열거형을 패턴 매칭하여 상태를 변경한다.
#[derive(Debug, Clone)]
pub enum Action {
    /// 애플리케이션 종료 (q 키)
    Quit,
    /// 화면 전환 (1-6 키)
    SwitchScreen(Screen),
    /// 목록에서 위로 이동 (k 또는 화살표 위)
    ScrollUp,
    /// 목록에서 아래로 이동 (j 또는 화살표 아래)
    ScrollDown,
    /// 현재 항목 선택/열기 (Enter)
    Select,
    /// 뒤로 가기/팝업 닫기 (Esc)
    Back,
    /// 검색 모드 진입 (/ 키, 현재 미구현)
    Search,
    /// 명령 모드 진입 (: 키, 현재 미구현)
    Command,
    /// 수동 새로고침 — 즉시 API 폴링 (r 키)
    Refresh,
    /// VM 시작 (s 키, VM Manager 화면 전용)
    VmStart,
    /// VM 중지 (x 키, VM Manager 화면 전용)
    VmStop,
    /// VM 일시정지 (p 키, VM Manager 화면 전용)
    VmPause,
    /// VM 삭제 (d 키, VM Manager 화면 전용)
    VmDelete,
    /// VM 생성 폼 열기 (c 키, VM Manager 화면 전용)
    CreateForm,
    /// 매핑되지 않은 키 (무시됨)
    None,
}

impl Action {
    /// 키 입력과 현재 화면을 기반으로 적절한 액션을 결정한다.
    ///
    /// # 매개변수
    /// - `key`: 눌린 키의 KeyCode
    /// - `screen`: 현재 활성 화면 (화면별 키 매핑에 사용)
    ///
    /// # 반환값
    /// - 해당 키에 매핑된 `Action`. 매핑이 없으면 `Action::None`
    pub fn from_key(key: KeyCode, screen: Screen) -> Self {
        match key {
            // ── 글로벌 키 (모든 화면에서 동작) ──
            KeyCode::Char('q') => Action::Quit,
            KeyCode::Char('1') => Action::SwitchScreen(Screen::Dashboard),
            KeyCode::Char('2') => Action::SwitchScreen(Screen::VmManager),
            KeyCode::Char('3') => Action::SwitchScreen(Screen::StorageView),
            KeyCode::Char('4') => Action::SwitchScreen(Screen::NetworkView),
            KeyCode::Char('5') => Action::SwitchScreen(Screen::LogViewer),
            KeyCode::Char('6') => Action::SwitchScreen(Screen::HaMonitor),
            KeyCode::Char('r') => Action::Refresh,

            // ── vim 스타일 네비게이션 ──
            KeyCode::Char('j') | KeyCode::Down => Action::ScrollDown,
            KeyCode::Char('k') | KeyCode::Up => Action::ScrollUp,
            KeyCode::Enter => Action::Select,
            KeyCode::Esc => Action::Back,
            KeyCode::Char('/') => Action::Search,
            KeyCode::Char(':') => Action::Command,

            // ── VM Manager 화면 전용 액션 ──
            KeyCode::Char('s') if screen == Screen::VmManager => Action::VmStart,
            KeyCode::Char('x') if screen == Screen::VmManager => Action::VmStop,
            KeyCode::Char('p') if screen == Screen::VmManager => Action::VmPause,
            KeyCode::Char('d') if screen == Screen::VmManager => Action::VmDelete,
            KeyCode::Char('c') if screen == Screen::VmManager => Action::CreateForm,

            _ => Action::None,
        }
    }
}
