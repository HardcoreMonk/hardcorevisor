//! VM Manager screen — live VM list with lifecycle actions

use crate::app::App;
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Cell, Clear, Paragraph, Row, Table},
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
        Span::styled("c", Style::default().fg(Color::Cyan)),
        Span::raw(" create  "),
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

    // ── Create VM Form Popup ──
    if app.show_create_form {
        render_create_form(frame, app, area);
    }
}

/// Render a centered popup for the VM creation form
fn render_create_form(frame: &mut Frame, app: &App, area: Rect) {
    let popup_width = 50u16.min(area.width.saturating_sub(4));
    let popup_height = 14u16.min(area.height.saturating_sub(4));
    let x = (area.width.saturating_sub(popup_width)) / 2;
    let y = (area.height.saturating_sub(popup_height)) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    // Clear background
    frame.render_widget(Clear, popup_area);

    let block = Block::default()
        .title(" Create VM ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));

    let inner = block.inner(popup_area);
    frame.render_widget(block, popup_area);

    let form = &app.create_form;
    let fields = [
        ("Name", &form.name),
        ("vCPUs", &form.vcpus),
        ("Memory (MB)", &form.memory_mb),
        ("Backend", &form.backend),
    ];

    let field_constraints: Vec<Constraint> = fields
        .iter()
        .map(|_| Constraint::Length(2))
        .chain(std::iter::once(Constraint::Length(1))) // spacer
        .chain(std::iter::once(Constraint::Length(1))) // error line
        .chain(std::iter::once(Constraint::Min(0))) // footer
        .collect();

    let field_chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints(field_constraints)
        .split(inner);

    for (i, (label, value)) in fields.iter().enumerate() {
        let is_focused = i == form.focused_field;
        let label_style = if is_focused {
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(Color::White)
        };
        let cursor = if is_focused { "_" } else { "" };
        let indicator = if is_focused { "> " } else { "  " };

        let line = Line::from(vec![
            Span::styled(indicator, label_style),
            Span::styled(format!("{label}: "), label_style),
            Span::styled(
                format!("{value}{cursor}"),
                Style::default().fg(Color::Yellow),
            ),
        ]);
        frame.render_widget(Paragraph::new(line), field_chunks[i]);
    }

    // Error line
    if let Some(ref err) = form.error {
        let err_line = Paragraph::new(Line::from(Span::styled(
            format!("  {err}"),
            Style::default().fg(Color::Red),
        )));
        frame.render_widget(err_line, field_chunks[fields.len() + 1]);
    }

    // Footer
    let footer_idx = field_chunks.len() - 1;
    let footer = Paragraph::new(Line::from(vec![
        Span::styled(" Tab", Style::default().fg(Color::Green)),
        Span::raw(": next  "),
        Span::styled("Enter", Style::default().fg(Color::Green)),
        Span::raw(": create  "),
        Span::styled("Esc", Style::default().fg(Color::Red)),
        Span::raw(": cancel"),
    ]));
    frame.render_widget(footer, field_chunks[footer_idx]);
}
