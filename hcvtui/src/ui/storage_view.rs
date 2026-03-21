//! Storage View screen — two-panel layout with pools and volumes

use crate::app::{App, ConnStatus};
use ratatui::{
    layout::{Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Cell, Gauge, Paragraph, Row, Table},
    Frame,
};

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
        Span::styled("Storage View", Style::default().fg(Color::Yellow)),
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
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(chunks[1]);

    // Left panel: Storage Pools
    render_pools_panel(frame, app, content_cols[0]);

    // Right panel: Volumes
    render_volumes_panel(frame, app, content_cols[1]);

    // -- Status Bar --
    let screen_labels = [
        ("1", "Dash", false),
        ("2", "VMs", false),
        ("3", "Storage", true),
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
    frame.render_widget(status, chunks[2]);
}

fn render_pools_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    if app.pools.is_empty() {
        let block = Block::default()
            .title(" Storage Pools (0) ")
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Blue));
        let content = Paragraph::new(Line::from(Span::styled(
            "  Waiting for data...",
            Style::default().fg(Color::DarkGray),
        )))
        .block(block);
        frame.render_widget(content, area);
        return;
    }

    // Split the pool area into table + gauge bars
    let pool_chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Min(3),                                 // table
            Constraint::Length(app.pools.len() as u16 * 2 + 2), // gauges
        ])
        .split(area);

    // Pool table
    let header_cells = ["NAME", "TYPE", "USED/TOTAL", "HEALTH", "% USED"]
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
        .pools
        .iter()
        .map(|pool| {
            let health_color = match pool.health.as_str() {
                "healthy" | "ONLINE" => Color::Green,
                "degraded" => Color::Yellow,
                _ => Color::Red,
            };
            let pct = if pool.total_bytes > 0 {
                (pool.used_bytes as f64 / pool.total_bytes as f64 * 100.0) as u64
            } else {
                0
            };
            let pct_color = if pct > 80 {
                Color::Red
            } else if pct > 60 {
                Color::Yellow
            } else {
                Color::Green
            };
            Row::new(vec![
                Cell::from(pool.name.as_str()),
                Cell::from(pool.pool_type.as_str()),
                Cell::from(format!(
                    "{}/{}",
                    format_bytes(pool.used_bytes),
                    format_bytes(pool.total_bytes)
                )),
                Cell::from(pool.health.as_str()).style(Style::default().fg(health_color)),
                Cell::from(format!("{pct}%")).style(Style::default().fg(pct_color)),
            ])
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Min(12),
            Constraint::Length(8),
            Constraint::Length(16),
            Constraint::Length(10),
            Constraint::Length(8),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(format!(" Storage Pools ({}) ", app.pools.len()))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Blue)),
    );
    frame.render_widget(table, pool_chunks[0]);

    // Usage gauges per pool
    let gauge_block = Block::default()
        .title(" Usage ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::DarkGray));
    let gauge_inner = gauge_block.inner(pool_chunks[1]);
    frame.render_widget(gauge_block, pool_chunks[1]);

    if gauge_inner.height > 0 {
        let gauge_rows = Layout::default()
            .direction(Direction::Vertical)
            .constraints(
                app.pools
                    .iter()
                    .map(|_| Constraint::Length(1))
                    .collect::<Vec<_>>(),
            )
            .split(gauge_inner);

        for (i, pool) in app.pools.iter().enumerate() {
            if i >= gauge_rows.len() {
                break;
            }
            let ratio = if pool.total_bytes > 0 {
                (pool.used_bytes as f64 / pool.total_bytes as f64).min(1.0)
            } else {
                0.0
            };
            let color = if ratio > 0.8 {
                Color::Red
            } else if ratio > 0.6 {
                Color::Yellow
            } else {
                Color::Green
            };
            let gauge = Gauge::default()
                .label(format!("{}: {:.0}%", pool.name, ratio * 100.0))
                .ratio(ratio)
                .gauge_style(Style::default().fg(color));
            frame.render_widget(gauge, gauge_rows[i]);
        }
    }
}

fn render_volumes_panel(frame: &mut Frame, app: &App, area: ratatui::layout::Rect) {
    let header_cells = ["ID", "POOL", "NAME", "SIZE", "FORMAT"].iter().map(|h| {
        Cell::from(*h).style(
            Style::default()
                .fg(Color::Yellow)
                .add_modifier(Modifier::BOLD),
        )
    });
    let header = Row::new(header_cells).height(1);

    let rows: Vec<Row> = app
        .volumes
        .iter()
        .map(|vol| {
            Row::new(vec![
                Cell::from(vol.id.as_str()),
                Cell::from(vol.pool.as_str()),
                Cell::from(vol.name.as_str()),
                Cell::from(format_bytes(vol.size_bytes)),
                Cell::from(vol.format.as_str()),
            ])
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Length(10),
            Constraint::Length(12),
            Constraint::Min(12),
            Constraint::Length(10),
            Constraint::Length(8),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(format!(" Volumes ({}) ", app.volumes.len()))
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Magenta)),
    );
    frame.render_widget(table, area);
}

fn format_bytes(bytes: u64) -> String {
    const GB: u64 = 1_073_741_824;
    const MB: u64 = 1_048_576;
    const KB: u64 = 1_024;
    if bytes >= GB {
        format!("{:.1} GB", bytes as f64 / GB as f64)
    } else if bytes >= MB {
        format!("{:.1} MB", bytes as f64 / MB as f64)
    } else if bytes >= KB {
        format!("{:.1} KB", bytes as f64 / KB as f64)
    } else {
        format!("{bytes} B")
    }
}
