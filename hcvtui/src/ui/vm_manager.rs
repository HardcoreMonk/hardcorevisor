//! VM Manager screen — live VM list with lifecycle actions

use crate::app::App;
use ratatui::{
    layout::{Constraint, Direction, Layout},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Cell, Paragraph, Row, Table},
    Frame,
};

pub fn render(frame: &mut Frame, app: &App) {
    let area = frame.area();

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3), // title bar
            Constraint::Min(0),    // VM table
            Constraint::Length(3), // help bar
        ])
        .split(area);

    // ── Title Bar ──
    let title = Paragraph::new(Line::from(vec![
        Span::styled(
            " HardCoreVisor ",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        ),
        Span::raw("│ "),
        Span::styled("VM Manager", Style::default().fg(Color::Yellow)),
        Span::raw(format!(" │ {} VMs", app.vms.len())),
    ]))
    .block(
        Block::default()
            .borders(Borders::BOTTOM)
            .border_style(Style::default().fg(Color::DarkGray)),
    );
    frame.render_widget(title, chunks[0]);

    // ── VM Table ──
    let header_cells = ["ID", "NAME", "STATE", "vCPUs", "MEMORY", "NODE", "BACKEND"]
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
        .vms
        .iter()
        .enumerate()
        .map(|(i, vm)| {
            let selected = i == app.vm_selected;
            let state_color = match vm.state.as_str() {
                "running" => Color::Green,
                "stopped" => Color::Red,
                "paused" => Color::Yellow,
                "configured" => Color::Blue,
                _ => Color::DarkGray,
            };
            let style = if selected {
                Style::default()
                    .bg(Color::DarkGray)
                    .add_modifier(Modifier::BOLD)
            } else {
                Style::default()
            };
            Row::new(vec![
                Cell::from(format!("{}", vm.id)),
                Cell::from(vm.name.as_str()),
                Cell::from(vm.state.as_str()).style(Style::default().fg(state_color)),
                Cell::from(format!("{}", vm.vcpus)),
                Cell::from(format!("{} MB", vm.memory_mb)),
                Cell::from(vm.node.as_str()),
                Cell::from(vm.backend.as_str()),
            ])
            .style(style)
        })
        .collect();

    let table = Table::new(
        rows,
        [
            Constraint::Length(5),
            Constraint::Min(15),
            Constraint::Length(12),
            Constraint::Length(6),
            Constraint::Length(10),
            Constraint::Length(10),
            Constraint::Length(10),
        ],
    )
    .header(header)
    .block(
        Block::default()
            .title(" Virtual Machines ")
            .borders(Borders::ALL)
            .border_style(Style::default().fg(Color::Magenta)),
    )
    .row_highlight_style(Style::default().add_modifier(Modifier::REVERSED));

    frame.render_widget(table, chunks[1]);

    // ── Help Bar ──
    let help = Paragraph::new(Line::from(vec![
        Span::styled(" j/k", Style::default().fg(Color::Green)),
        Span::raw(" navigate  "),
        Span::styled("s", Style::default().fg(Color::Green)),
        Span::raw(" start  "),
        Span::styled("x", Style::default().fg(Color::Red)),
        Span::raw(" stop  "),
        Span::styled("p", Style::default().fg(Color::Yellow)),
        Span::raw(" pause  "),
        Span::styled("d", Style::default().fg(Color::Red)),
        Span::raw(" delete  "),
        Span::styled("r", Style::default().fg(Color::Cyan)),
        Span::raw(" refresh  "),
        Span::styled("1-6", Style::default().fg(Color::White)),
        Span::raw(" switch  "),
        Span::styled("q", Style::default().fg(Color::Red)),
        Span::raw(" quit"),
    ]))
    .block(
        Block::default()
            .borders(Borders::TOP)
            .border_style(Style::default().fg(Color::DarkGray)),
    );
    frame.render_widget(help, chunks[2]);
}
