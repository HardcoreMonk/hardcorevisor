//! 대시보드 화면 — 4패널 레이아웃으로 클러스터 개요를 표시한다.
//!
//! Go Controller REST API에서 실시간으로 가져온 데이터를 표시한다.
//!
//! ## 레이아웃 구조
//!
//! ```text
//! ┌─── 타이틀 바 ────────────────────────────┐
//! │  HardCoreVisor │ Dashboard │ ● Connected  │
//! ├───────────────────┬──────────────────────┤
//! │  Cluster Nodes    │  Virtual Machines     │  ← 상단 2열
//! │  (노드 상태/CPU)  │  (상태별 VM 수)       │
//! ├───────────────────┼──────────────────────┤
//! │  System Info      │  Status               │  ← 하단 2열
//! │  (버전/아키텍처)  │  (API/WS 상태/에러)   │
//! ├───────────────────┴──────────────────────┤
//! │  [1]Dash [2]VMs [3]Storage ...           │  ← 상태 바
//! └──────────────────────────────────────────┘
//! ```

use ratatui::{
    layout::{Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph},
    Frame,
};

use crate::app::{App, ConnStatus};

/// 대시보드 화면을 렌더링한다.
///
/// # 매개변수
/// - `frame`: Ratatui 프레임 (위젯을 그리는 캔버스)
/// - `app`: 애플리케이션 상태 (API 데이터, 연결 상태 등)
pub fn render(frame: &mut Frame, app: &App) {
    let area = frame.area();

    let main_chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3), // title bar
            Constraint::Min(0),    // content
            Constraint::Length(1), // status bar
        ])
        .split(area);

    // ── Title Bar ──
    let conn_indicator = match app.conn_status {
        ConnStatus::Connected => Span::styled("● Connected", Style::default().fg(Color::Green)),
        ConnStatus::Disconnected => {
            Span::styled("○ Disconnected", Style::default().fg(Color::DarkGray))
        }
        ConnStatus::Error => Span::styled("● Error", Style::default().fg(Color::Red)),
    };

    let ws_indicator = if app.ws_available {
        Span::styled("WS Ready", Style::default().fg(Color::Green))
    } else {
        Span::styled("WS N/A", Style::default().fg(Color::DarkGray))
    };

    let title = Paragraph::new(Line::from(vec![
        Span::styled(
            " HardCoreVisor ",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw("│ "),
        Span::styled("Dashboard", Style::default().fg(Color::Yellow)),
        Span::raw(" │ "),
        conn_indicator,
        Span::raw(" │ "),
        ws_indicator,
        Span::raw(" │ "),
        Span::styled("r", Style::default().fg(Color::Green)),
        Span::raw(" refresh  "),
        Span::styled("q", Style::default().fg(Color::Red)),
        Span::raw(" quit  "),
        Span::styled("1-6", Style::default().fg(Color::Green)),
        Span::raw(" switch"),
    ]))
    .block(
        Block::default()
            .borders(Borders::BOTTOM)
            .border_style(Style::default().fg(Color::DarkGray)),
    );
    frame.render_widget(title, main_chunks[0]);

    // ── Content Area (2x2 grid) ──
    let content_rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(main_chunks[1]);

    let top_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(content_rows[0]);

    let bot_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(content_rows[1]);

    // Panel 1: Cluster Nodes (live data)
    render_nodes_panel(frame, app, top_cols[0]);

    // Panel 2: VM Summary (live data)
    render_vm_summary_panel(frame, app, top_cols[1]);

    // Panel 3: System Info
    render_system_info_panel(frame, app, bot_cols[0]);

    // Panel 4: Connection Status / Errors
    render_status_panel(frame, app, bot_cols[1]);

    // ── Status Bar ──
    let screen_labels = [
        ("1", "Dash", true),
        ("2", "VMs", false),
        ("3", "Storage", false),
        ("4", "Network", false),
        ("5", "Logs", false),
        ("6", "HA", false),
    ];
    let mut spans = Vec::new();
    for (key, label, active) in screen_labels {
        let color = if active { Color::Cyan } else { Color::White };
        spans.push(Span::styled(
            format!(" [{key}]"),
            Style::default().fg(color),
        ));
        spans.push(Span::raw(format!("{label} ")));
    }
    let status = Paragraph::new(Line::from(spans))
        .style(Style::default().bg(Color::DarkGray).fg(Color::White));
    frame.render_widget(status, main_chunks[2]);
}

/// 클러스터 노드 패널을 렌더링한다.
///
/// 각 노드의 온라인/오프라인 상태, CPU/메모리 사용률, VM 수를 표시한다.
/// 사용률이 60% 초과 시 노란색, 80% 초과 시 빨간색으로 표시한다.
fn render_nodes_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(format!(" Cluster Nodes ({}) ", app.nodes.len()))
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Blue));

    let mut lines = Vec::new();
    if app.nodes.is_empty() {
        lines.push(Line::from(Span::styled(
            "  Waiting for data...",
            Style::default().fg(Color::DarkGray),
        )));
    } else {
        for node in &app.nodes {
            let status_color = match node.status.as_str() {
                "online" => Color::Green,
                "offline" => Color::Red,
                _ => Color::Yellow,
            };
            let cpu_color = if node.cpu_percent > 80.0 {
                Color::Red
            } else if node.cpu_percent > 60.0 {
                Color::Yellow
            } else {
                Color::Green
            };
            let mem_color = if node.memory_percent > 80.0 {
                Color::Red
            } else if node.memory_percent > 60.0 {
                Color::Yellow
            } else {
                Color::Green
            };
            lines.push(Line::from(vec![
                Span::styled("●", Style::default().fg(status_color)),
                Span::raw(format!(" {:<10}", node.name)),
                Span::styled(
                    format!("CPU:{:>4.0}%", node.cpu_percent),
                    Style::default().fg(cpu_color),
                ),
                Span::raw("  "),
                Span::styled(
                    format!("MEM:{:>4.0}%", node.memory_percent),
                    Style::default().fg(mem_color),
                ),
                Span::raw(format!("  VMs:{}", node.vm_count)),
            ]));
        }
    }

    frame.render_widget(Paragraph::new(lines).block(block), area);
}

/// VM 요약 패널을 렌더링한다.
///
/// 상태별(running/stopped/paused/configured) VM 수와 전체 합계를 표시한다.
fn render_vm_summary_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(format!(" Virtual Machines ({}) ", app.vms.len()))
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Magenta));

    let running = app.vms.iter().filter(|v| v.state == "running").count();
    let stopped = app.vms.iter().filter(|v| v.state == "stopped").count();
    let paused = app.vms.iter().filter(|v| v.state == "paused").count();
    let configured = app.vms.iter().filter(|v| v.state == "configured").count();

    let lines = if app.vms.is_empty() {
        vec![Line::from(Span::styled(
            "  No VMs",
            Style::default().fg(Color::DarkGray),
        ))]
    } else {
        vec![
            Line::from(vec![
                Span::styled("  ▶ Running:    ", Style::default().fg(Color::Green)),
                Span::raw(format!("{running}")),
            ]),
            Line::from(vec![
                Span::styled("  ■ Stopped:    ", Style::default().fg(Color::Red)),
                Span::raw(format!("{stopped}")),
            ]),
            Line::from(vec![
                Span::styled("  ⏸ Paused:     ", Style::default().fg(Color::Yellow)),
                Span::raw(format!("{paused}")),
            ]),
            Line::from(vec![
                Span::styled("  ◉ Configured: ", Style::default().fg(Color::Blue)),
                Span::raw(format!("{configured}")),
            ]),
            Line::from(""),
            Line::from(vec![
                Span::raw("  Total:        "),
                Span::styled(
                    format!("{}", app.vms.len()),
                    Style::default().add_modifier(Modifier::BOLD),
                ),
            ]),
        ]
    };

    frame.render_widget(Paragraph::new(lines).block(block), area);
}

/// 시스템 정보 패널을 렌더링한다.
///
/// Controller 버전, 제품명, 아키텍처, vmcore 버전을 표시한다.
/// 데이터 로드 전에는 "Loading..." 메시지를 표시한다.
fn render_system_info_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(" System Info ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));

    let ver = &app.version;
    let lines = if ver.version.is_empty() {
        vec![Line::from(Span::styled(
            "  Loading...",
            Style::default().fg(Color::DarkGray),
        ))]
    } else {
        vec![
            Line::from(vec![
                Span::styled("  Product:  ", Style::default().fg(Color::DarkGray)),
                Span::styled(
                    &ver.product,
                    Style::default()
                        .fg(Color::Cyan)
                        .add_modifier(Modifier::BOLD),
                ),
            ]),
            Line::from(vec![
                Span::styled("  Version:  ", Style::default().fg(Color::DarkGray)),
                Span::raw(&ver.version),
            ]),
            Line::from(vec![
                Span::styled("  Arch:     ", Style::default().fg(Color::DarkGray)),
                Span::raw(&ver.arch),
            ]),
            Line::from(vec![
                Span::styled("  VMCore:   ", Style::default().fg(Color::DarkGray)),
                Span::raw(&ver.vmcore_version),
            ]),
        ]
    };

    frame.render_widget(Paragraph::new(lines).block(block), area);
}

/// 연결 상태 패널을 렌더링한다.
///
/// REST API 및 WebSocket 연결 상태를 표시한다.
/// 에러가 발생한 경우 에러 메시지도 빨간색으로 표시한다.
fn render_status_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(" Status ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Yellow));

    let mut lines = vec![
        Line::from(vec![
            Span::styled("  API: ", Style::default().fg(Color::DarkGray)),
            match app.conn_status {
                ConnStatus::Connected => {
                    Span::styled("Connected", Style::default().fg(Color::Green))
                }
                ConnStatus::Disconnected => {
                    Span::styled("Disconnected", Style::default().fg(Color::DarkGray))
                }
                ConnStatus::Error => Span::styled("Error", Style::default().fg(Color::Red)),
            },
        ]),
        Line::from(vec![
            Span::styled("  WS:  ", Style::default().fg(Color::DarkGray)),
            if app.ws_available {
                Span::styled("Ready", Style::default().fg(Color::Green))
            } else {
                Span::styled("Unavailable", Style::default().fg(Color::DarkGray))
            },
        ]),
    ];

    if let Some(err) = &app.last_error {
        lines.push(Line::from(""));
        lines.push(Line::from(vec![
            Span::styled("  ", Style::default()),
            Span::styled(err.as_str(), Style::default().fg(Color::Red)),
        ]));
    }

    frame.render_widget(Paragraph::new(lines).block(block), area);
}
