//! # hcvtui — HardCoreVisor 터미널 UI
//!
//! Ratatui + Crossterm 기반의 관리 콘솔 애플리케이션이다.
//! Go Controller REST API에 연결하여 VM, 스토리지, 네트워크, HA 클러스터 상태를
//! 실시간으로 모니터링하고 제어한다.
//!
//! ## 아키텍처 개요
//!
//! ```text
//! hcvtui (이 크레이트)
//!     ├── main.rs          -- 엔트리포인트: tokio 런타임 초기화 + 터미널 설정
//!     ├── app.rs           -- App 상태 + 즉시 모드(Immediate Mode) 이벤트 루프
//!     ├── api_client.rs    -- reqwest 기반 REST API 클라이언트
//!     ├── keybindings.rs   -- vim 스타일 키바인딩 → Action 매핑
//!     └── ui/              -- 6개 화면 렌더링 모듈
//! ```
//!
//! ## 즉시 모드 렌더링 패턴 (Immediate Mode Rendering)
//!
//! 매 프레임마다 전체 UI를 다시 그린다. 상태 변경이 있든 없든
//! `render()` → `handle_input()` → `tick()` 순서로 반복한다.
//! Ratatui가 이전 프레임과의 차이(diff)만 실제 터미널에 출력하므로
//! 깜빡임 없이 효율적으로 동작한다.

mod api_client;
mod app;
mod keybindings;
mod ui;

use color_eyre::Result;

/// 프로그램 엔트리포인트
///
/// 1. `color_eyre` 에러 핸들러를 설치한다 (패닉 시 보기 좋은 에러 출력).
/// 2. `tracing_subscriber`로 디버그 로깅을 초기화한다.
/// 3. Ratatui 터미널을 설정하고 App 이벤트 루프를 실행한다.
/// 4. 종료 시 터미널 원래 상태를 복원한다 (`ratatui::restore()`).
#[tokio::main]
async fn main() -> Result<()> {
    color_eyre::install()?;
    tracing_subscriber::fmt()
        .with_env_filter("hcvtui=debug")
        .init();

    // Ratatui 터미널 초기화: raw 모드 + alternate screen 진입
    let mut terminal = ratatui::init();
    let result = app::App::new().run(&mut terminal).await;
    // 종료 시 반드시 터미널 복원 (커서 표시, raw 모드 해제 등)
    ratatui::restore();
    result
}
