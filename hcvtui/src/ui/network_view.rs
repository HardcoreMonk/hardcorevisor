//! 네트워크 뷰 화면 — SDN 존, 가상 네트워크, 방화벽 규칙을 표시한다.
//!
//! ## 레이아웃 구조
//!
//! ```text
//! ┌─── 타이틀 바 ────────────────────────────┐
//! │  HardCoreVisor │ Network View │ Connected │
//! ├──────────────────────────────────────────┤
//! │  SDN Zones (40%)                         │  ← 수직 3단 분할
//! ├──────────────────────────────────────────┤
//! │  Virtual Networks (40%)                  │
//! ├──────────────────────────────────────────┤
//! │  Firewall Rules (5줄)                    │
//! ├──────────────────────────────────────────┤
//! │  [1]Dash [2]VMs [3]Storage ...           │
//! └──────────────────────────────────────────┘
//! ```

use crate::app::{App, ConnStatus};
use ratatui::{
    layout::{Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Cell, Paragraph, Row, Table},
    Frame,
};

/// 네트워크 뷰 화면을 렌더링한다.
///
/// SDN 존 테이블, 가상 네트워크 테이블, 방화벽 요약의 수직 3단 구조이다.
pub fn render(frame: &mut Frame, app: &App) {
    let area = frame.area();

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3), // title bar
            Constraint::Min(0),    // content
            Constraint::Length(1), // status bar
        ])
        .split(area);

    // -- Title Bar --
    let conn_indicator = match app.conn_status {
        ConnStatus::Connected => Span::styled("● Connected", Style::default().fg(Color::Green)),
        ConnStatus::Disconnected => {
            Span::styled("○ Disconnected", Style::default().fg(Color::DarkGray))
        }
        ConnStatus::Error => Span::styled("● Error", Style::default().fg(Color::Red)),
    };

    let title = Paragraph::new(Line::from(vec![
        Span::styled(
            " HardCoreVisor ",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw("│ "),
        Span::styled("Network View", Style::default().fg(Color::Yellow)),
        Span::raw(" │ "),
        conn_indicator,
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
    frame.render_widget(title, chunks[0]);

    // -- Content: three sections vertically --
    let content_sections = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Percentage(40), // SDN Zones
            Constraint::Percentage(40), // Virtual Networks
            Constraint::Length(5),      // Firewall summary
        ])
        .split(chunks[1]);

    // Top: SDN Zones table
    render_zones_table(frame, app, content_sections[0]);

    // Middle: Virtual Networks table
    render_vnets_table(frame, app, content_sections[1]);

    // Bottom: Firewall Rules summary
    render_firewall_summary(frame, content_sections[2]);

    // -- Status Bar --
    let screen_labels = [
        ("1", "Dash", false),
        ("2", "VMs", false),
        ("3", "Storage", false),
        ("4", "Network", true),
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
    frame.render_widget(status, chunks[2]);
}

/// SDN 존 테이블을 렌더링한다 (이름, 타입, MTU, 브리지, 상태).
fn render_zones_table(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let header_cells = ["NAME", "TYPE", "MTU", "BRIDGE", "STATUS"].iter().map(|h| {
        Cell::from(*h).style(
            Style::default()
                .fg(Color::Yellow)
                .add_modifier(Modifier::BOLD),
        )
    });
    let header = Row::new(header_cells).height(1);

    let rows: Vec<Row> = app
        .zones
        .iter()
        .map(|zone| {
            let status_color = match zone.status.as_str() {
                "active" => Color::Green,
                "inactive" | "down" => Color::Red,
                _ => Color::Yellow,
            };
            Row::new(vec![
                Cell::from(zone.name.as_str()),
                Cell::from(zone.zone_type.as_str()),
                Cell::from(format!("{}", zone.mtu)),
                Cell::from(zone.bridge.as_str()),
                Cell::from(zone.status.as_str()).style(Style::default().fg(status_color)),
            ])
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Min(14),
            Constraint::Length(10),
            Constraint::Length(8),
            Constraint::Length(12),
            Constraint::Length(10),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(format!(" SDN Zones ({}) ", app.zones.len()))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Blue)),
    );
    frame.render_widget(table, area);
}

/// 가상 네트워크 테이블을 렌더링한다 (ID, 존, 이름, 태그, 서브넷, 상태).
fn render_vnets_table(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let header_cells = ["ID", "ZONE", "NAME", "TAG", "SUBNET", "STATUS"]
        .iter()
        .map(|h| {
            Cell::from(*h).style(
                Style::default()
                    .fg(Color::Yellow)
                    .add_modifier(Modifier::BOLD),
            )
        });
    let header = Row::new(header_cells).height(1);

    let rows: Vec<Row> = app
        .vnets
        .iter()
        .map(|vnet| {
            let status_color = match vnet.status.as_str() {
                "active" => Color::Green,
                "inactive" | "down" => Color::Red,
                _ => Color::Yellow,
            };
            Row::new(vec![
                Cell::from(vnet.id.as_str()),
                Cell::from(vnet.zone.as_str()),
                Cell::from(vnet.name.as_str()),
                Cell::from(format!("{}", vnet.tag)),
                Cell::from(vnet.subnet.as_str()),
                Cell::from(vnet.status.as_str()).style(Style::default().fg(status_color)),
            ])
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Length(10),
            Constraint::Length(12),
            Constraint::Min(14),
            Constraint::Length(6),
            Constraint::Length(18),
            Constraint::Length(10),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(format!(" Virtual Networks ({}) ", app.vnets.len()))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Green)),
    );
    frame.render_widget(table, area);
}

/// 방화벽 규칙 요약을 렌더링한다.
///
/// 현재는 "No rules configured" 메시지만 표시한다.
/// 방화벽 규칙은 Controller API를 통해 관리한다.
fn render_firewall_summary(frame: &mut Frame, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(" Firewall Rules ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Red));

    let content = Paragraph::new(vec![
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled("No rules configured", Style::default().fg(Color::DarkGray)),
            Span::raw("  (firewall rules are managed via the Controller API)"),
        ]),
    ])
    .block(block);
    frame.render_widget(content, area);
}
