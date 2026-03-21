//! Log Viewer screen — scrollable recent events log

use crate::app::{App, ConnStatus};
use ratatui::{
    layout::{Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph},
    Frame,
};

pub fn render(frame: &mut Frame, app: &App) {
    let area = frame.area();

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3), // title bar
            Constraint::Min(0),    // log content
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
        Span::styled("Log Viewer", Style::default().fg(Color::Yellow)),
        Span::raw(format!(" │ {} entries", app.log_entries.len())),
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

    // -- Log Content --
    let log_block = Block::default()
        .title(" Event Log ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Yellow));

    let lines: Vec<Line> = app
        .log_entries
        .iter()
        .map(|entry| {
            let color = if entry.contains("[ERROR]") {
                Color::Red
            } else if entry.contains("[EVENT]") {
                Color::Cyan
            } else if entry.contains("[WARN]") {
                Color::Yellow
            } else {
                Color::White
            };
            Line::from(Span::styled(
                format!("  {entry}"),
                Style::default().fg(color),
            ))
        })
        .collect();

    // Auto-scroll: compute scroll offset so the bottom of the log is visible
    let inner_height = chunks[1].height.saturating_sub(2); // block borders
    let total_lines = lines.len() as u16;
    let scroll = total_lines.saturating_sub(inner_height);

    let log_paragraph = Paragraph::new(lines).block(log_block).scroll((scroll, 0));
    frame.render_widget(log_paragraph, chunks[1]);

    // -- Status Bar --
    let screen_labels = [
        ("1", "Dash", false),
        ("2", "VMs", false),
        ("3", "Storage", false),
        ("4", "Network", false),
        ("5", "Logs", true),
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
