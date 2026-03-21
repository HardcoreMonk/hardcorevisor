//! Network View screen — placeholder

use crate::app::App;
use ratatui::{
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph},
    Frame,
};

pub fn render(frame: &mut Frame, _app: &App) {
    let block = Block::default()
        .title(" Network View ")
        .borders(Borders::ALL)
        .border_style(Style::default().fg(Color::Cyan));
    let content = Paragraph::new(vec![
        Line::from(""),
        Line::from(vec![Span::styled(
            "  Network View",
            Style::default()
                .fg(Color::Cyan)
                .add_modifier(Modifier::BOLD),
        )]),
        Line::from("  This screen is under construction."),
        Line::from(""),
        Line::from("  Press [1]-[6] to switch screens, [q] to quit."),
    ])
    .block(block);
    frame.render_widget(content, frame.area());
}
