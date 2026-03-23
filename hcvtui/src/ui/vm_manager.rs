//! VM 매니저 화면 — VM 테이블과 생명주기 제어
//!
//! ## 레이아웃 구조
//!
//! ```text
//! ┌─── 타이틀 바 ────────────────────────────┐
//! │  HardCoreVisor │ VM Manager │ N VMs      │
//! ├──────────────────────────────────────────┤
//! │  ID  NAME      STATE   vCPUs MEMORY ...  │  ← VM 테이블
//! │  1   web-01    running  4    8192 MB     │
//! │  2   db-01     stopped  8    32768 MB    │  ← j/k로 선택 이동
//! ├──────────────────────────────────────────┤
//! │  j/k navigate  s start  x stop  ...     │  ← 도움말 바
//! └──────────────────────────────────────────┘
//! ```
//!
//! ## 팝업
//! - Enter: VM 상세 팝업 (ID, 이름, 상태, 리소스 정보)
//! - c: VM 생성 폼 팝업 (이름, vCPU, 메모리, 백엔드 입력)

use crate::app::App;
use ratatui::{
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Cell, Clear, Paragraph, Row, Table},
    Frame,
};

/// VM 매니저 화면을 렌더링한다.
///
/// 타이틀 바, VM 테이블, 도움말 바의 3단 레이아웃으로 구성된다.
/// VM 상세 또는 생성 폼 팝업이 활성화되어 있으면 테이블 위에 오버레이로 표시한다.
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
    // Phase 16: TYPE 컬럼 추가 — "VM"(녹색) 또는 "CT"(시안)으로 워크로드 유형을 구분
    let header_cells = ["ID", "NAME", "TYPE", "STATE", "vCPUs", "MEMORY", "NODE", "BACKEND"]
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
            // Phase 16: TYPE 컬럼 렌더링
            // vm_type이 "container"이면 "CT" (시안색), 그 외에는 "VM" (녹색)으로 표시한다.
            // api_client.rs의 VmInfo.vm_type 필드에서 가져온다.
            let is_container = vm.vm_type == "container";
            let type_label = if is_container { "CT" } else { "VM" };
            let type_color = if is_container {
                Color::Cyan
            } else {
                Color::Green
            };
            Row::new(vec![
                Cell::from(format!("{}", vm.id)),
                Cell::from(vm.name.as_str()),
                Cell::from(type_label).style(Style::default().fg(type_color)),
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
            Constraint::Min(12),
            Constraint::Length(4),
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

    // ── VM Detail Popup ──
    if app.show_vm_detail {
        render_vm_detail(frame, app, area);
    }

    // ── Create VM Form Popup ──
    if app.show_create_form {
        render_create_form(frame, app, area);
    }
}

/// 중앙 정렬된 VM 상세 팝업을 렌더링한다.
///
/// 선택된 VM의 ID, 이름, 상태, vCPU, 메모리, 노드, 백엔드를 표시한다.
/// 화면 크기의 60% x 70% 영역을 사용하며, Esc 또는 Enter로 닫는다.
fn render_vm_detail(frame: &mut Frame, app: &App, area: Rect) {
    let vm = match app.vms.get(app.vm_selected) {
        Some(vm) => vm,
        None => return,
    };

    let popup_width = (area.width * 60 / 100)
        .max(40)
        .min(area.width.saturating_sub(4));
    let popup_height = (area.height * 70 / 100)
        .max(12)
        .min(area.height.saturating_sub(4));
    let x = (area.width.saturating_sub(popup_width)) / 2;
    let y = (area.height.saturating_sub(popup_height)) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    frame.render_widget(Clear, popup_area);

    let block = Block::default()
        .title(" VM Detail ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));

    let inner = block.inner(popup_area);
    frame.render_widget(block, popup_area);

    let state_color = match vm.state.as_str() {
        "running" => Color::Green,
        "stopped" => Color::Red,
        "paused" => Color::Yellow,
        "configured" => Color::Blue,
        _ => Color::DarkGray,
    };

    let label_style = Style::default()
        .fg(Color::Yellow)
        .add_modifier(Modifier::BOLD);
    let value_style = Style::default().fg(Color::White);

    let lines = vec![
        Line::from(vec![
            Span::styled("  ID:        ", label_style),
            Span::styled(format!("{}", vm.id), value_style),
        ]),
        Line::from(vec![
            Span::styled("  Name:      ", label_style),
            Span::styled(vm.name.clone(), value_style),
        ]),
        Line::from(vec![
            Span::styled("  State:     ", label_style),
            Span::styled(vm.state.clone(), Style::default().fg(state_color)),
        ]),
        Line::from(vec![
            Span::styled("  vCPUs:     ", label_style),
            Span::styled(format!("{}", vm.vcpus), value_style),
        ]),
        Line::from(vec![
            Span::styled("  Memory:    ", label_style),
            Span::styled(format!("{} MB", vm.memory_mb), value_style),
        ]),
        Line::from(vec![
            Span::styled("  Node:      ", label_style),
            Span::styled(vm.node.clone(), value_style),
        ]),
        Line::from(vec![
            Span::styled("  Backend:   ", label_style),
            Span::styled(vm.backend.clone(), value_style),
        ]),
    ];

    let detail_chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Min(0),    // detail content
            Constraint::Length(1), // footer
        ])
        .split(inner);

    let detail = Paragraph::new(lines);
    frame.render_widget(detail, detail_chunks[0]);

    let footer = Paragraph::new(Line::from(vec![
        Span::styled(" Esc", Style::default().fg(Color::Green)),
        Span::raw(": close"),
    ]));
    frame.render_widget(footer, detail_chunks[1]);
}

/// VM/컨테이너 생성 폼 팝업을 렌더링한다.
///
/// 5개 필드(Name, vCPUs, Memory, Backend, Type)를 세로로 배치하며,
/// 현재 포커스된 필드에 "> " 인디케이터와 커서("_")를 표시한다.
/// Tab으로 필드 이동, Enter로 생성, Esc로 취소한다.
///
/// Phase 16: Type 필드가 추가되어 "vm" 또는 "container"를 선택할 수 있다.
/// Type이 "container"이면 팝업 제목이 "Create Container"로 변경된다.
fn render_create_form(frame: &mut Frame, app: &App, area: Rect) {
    let popup_width = 50u16.min(area.width.saturating_sub(4));
    let popup_height = 16u16.min(area.height.saturating_sub(4));
    let x = (area.width.saturating_sub(popup_width)) / 2;
    let y = (area.height.saturating_sub(popup_height)) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    // Clear background
    frame.render_widget(Clear, popup_area);

    // Phase 16: workload_type에 따라 팝업 제목을 동적으로 변경한다.
    let is_container = app.create_form.workload_type.trim() == "container";
    let title = if is_container {
        " Create Container "
    } else {
        " Create VM "
    };
    let block = Block::default()
        .title(title)
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
        ("Type", &form.workload_type),
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
