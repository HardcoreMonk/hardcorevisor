//! HA 모니터 화면 — 클러스터 상태와 노드 목록을 표시한다.
//!
//! ## 레이아웃 구조
//!
//! ```text
//! ┌─── 타이틀 바 ────────────────────────────┐
//! │  HardCoreVisor │ HA Monitor │ Connected   │
//! ├─────────────────┬────────────────────────┤
//! │  Cluster Status │  Cluster Nodes          │  ← 좌40:우60 분할
//! │  Quorum: YES    │  NAME  STATUS LEADER VM │
//! │  Leader: node-1 │  node-01 online * YES 2 │
//! │  Nodes: 3/3     │  node-02 online   no  1 │
//! │  Health: healthy│  node-03 online   no  0 │
//! ├─────────────────┴────────────────────────┤
//! │  [1]Dash [2]VMs ... [6]HA                │
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

/// HA 모니터 화면을 렌더링한다.
///
/// 좌측에 클러스터 전체 상태(쿼럼, 리더, 헬스), 우측에 노드 테이블을 표시한다.
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
        Span::styled("HA Monitor", Style::default().fg(Color::Yellow)),
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

    // -- Content: two panels side by side --
    let content_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(40), Constraint::Percentage(60)])
        .split(chunks[1]);

    // Left: Cluster status summary
    render_cluster_status(frame, app, content_cols[0]);

    // Right: Node list table
    render_node_list(frame, app, content_cols[1]);

    // -- Status Bar --
    let screen_labels = [
        ("1", "Dash", false),
        ("2", "VMs", false),
        ("3", "Storage", false),
        ("4", "Network", false),
        ("5", "Logs", false),
        ("6", "HA", true),
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

/// 클러스터 상태 요약 패널을 렌더링한다.
///
/// 쿼럼 유지 여부, 리더 노드, 온라인/전체 노드 수, 헬스 상태를 표시한다.
/// 클러스터 데이터가 아직 없으면 "Waiting for cluster data..." 메시지를 표시한다.
fn render_cluster_status(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let block = Block::default()
        .title(" Cluster Status ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));

    let lines = if let Some(ref cluster) = app.cluster {
        let quorum_color = if cluster.quorum {
            Color::Green
        } else {
            Color::Red
        };
        let quorum_text = if cluster.quorum { "YES" } else { "NO" };

        let health_color = match cluster.status.as_str() {
            "healthy" => Color::Green,
            "degraded" => Color::Yellow,
            _ => Color::Red,
        };

        vec![
            Line::from(""),
            Line::from(vec![
                Span::styled("  Quorum:     ", Style::default().fg(Color::DarkGray)),
                Span::styled(
                    quorum_text,
                    Style::default()
                        .fg(quorum_color)
                        .add_modifier(Modifier::BOLD),
                ),
            ]),
            Line::from(""),
            Line::from(vec![
                Span::styled("  Leader:     ", Style::default().fg(Color::DarkGray)),
                Span::styled(
                    format!("* {}", cluster.leader),
                    Style::default()
                        .fg(Color::Yellow)
                        .add_modifier(Modifier::BOLD),
                ),
            ]),
            Line::from(""),
            Line::from(vec![
                Span::styled("  Nodes:      ", Style::default().fg(Color::DarkGray)),
                Span::styled(
                    format!("{}", cluster.online_count),
                    Style::default().fg(Color::Green),
                ),
                Span::raw(format!(" / {}", cluster.node_count)),
                Span::styled(" online", Style::default().fg(Color::DarkGray)),
            ]),
            Line::from(""),
            Line::from(vec![
                Span::styled("  Health:     ", Style::default().fg(Color::DarkGray)),
                Span::styled(
                    cluster.status.as_str(),
                    Style::default()
                        .fg(health_color)
                        .add_modifier(Modifier::BOLD),
                ),
            ]),
        ]
    } else {
        vec![
            Line::from(""),
            Line::from(Span::styled(
                "  Waiting for cluster data...",
                Style::default().fg(Color::DarkGray),
            )),
        ]
    };

    frame.render_widget(Paragraph::new(lines).block(block), area);
}

/// 클러스터 노드 테이블을 렌더링한다.
///
/// 노드 이름, 상태, 리더 여부, VM 수, 펜스 에이전트를 표시한다.
/// 상태 색상: online=초록, offline=빨강, fenced=노랑
fn render_node_list(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let header_cells = ["NAME", "STATUS", "LEADER", "VMs", "FENCE AGENT"]
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
        .cluster_nodes
        .iter()
        .map(|node| {
            let status_color = match node.status.as_str() {
                "online" => Color::Green,
                "offline" => Color::Red,
                "fenced" => Color::Yellow,
                _ => Color::DarkGray,
            };
            let leader_indicator = if node.is_leader { "* YES" } else { "  no" };
            let leader_color = if node.is_leader {
                Color::Yellow
            } else {
                Color::DarkGray
            };

            Row::new(vec![
                Cell::from(node.name.as_str()),
                Cell::from(node.status.as_str()).style(Style::default().fg(status_color)),
                Cell::from(leader_indicator).style(Style::default().fg(leader_color)),
                Cell::from(format!("{}", node.vm_count)),
                Cell::from(node.fence_agent.as_str()),
            ])
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Min(12),
            Constraint::Length(10),
            Constraint::Length(8),
            Constraint::Length(6),
            Constraint::Length(16),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(format!(" Cluster Nodes ({}) ", app.cluster_nodes.len()))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Green)),
    );
    frame.render_widget(table, area);
}
