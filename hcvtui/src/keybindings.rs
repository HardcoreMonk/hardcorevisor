//! Keybinding definitions — vim-style navigation

use crate::app::Screen;
use crossterm::event::KeyCode;

/// Actions triggered by key presses
#[derive(Debug, Clone)]
pub enum Action {
    Quit,
    SwitchScreen(Screen),
    ScrollUp,
    ScrollDown,
    Select,
    Back,
    Search,
    Command,
    Refresh,
    VmStart,
    VmStop,
    VmPause,
    VmDelete,
    None,
}

impl Action {
    /// Map a key press to an action, considering the current screen.
    pub fn from_key(key: KeyCode, screen: Screen) -> Self {
        match key {
            // Global keys
            KeyCode::Char('q') => Action::Quit,
            KeyCode::Char('1') => Action::SwitchScreen(Screen::Dashboard),
            KeyCode::Char('2') => Action::SwitchScreen(Screen::VmManager),
            KeyCode::Char('3') => Action::SwitchScreen(Screen::StorageView),
            KeyCode::Char('4') => Action::SwitchScreen(Screen::NetworkView),
            KeyCode::Char('5') => Action::SwitchScreen(Screen::LogViewer),
            KeyCode::Char('6') => Action::SwitchScreen(Screen::HaMonitor),
            KeyCode::Char('r') => Action::Refresh,

            // vim-style navigation
            KeyCode::Char('j') | KeyCode::Down => Action::ScrollDown,
            KeyCode::Char('k') | KeyCode::Up => Action::ScrollUp,
            KeyCode::Enter => Action::Select,
            KeyCode::Esc => Action::Back,
            KeyCode::Char('/') => Action::Search,
            KeyCode::Char(':') => Action::Command,

            // VM Manager actions
            KeyCode::Char('s') if screen == Screen::VmManager => Action::VmStart,
            KeyCode::Char('x') if screen == Screen::VmManager => Action::VmStop,
            KeyCode::Char('p') if screen == Screen::VmManager => Action::VmPause,
            KeyCode::Char('d') if screen == Screen::VmManager => Action::VmDelete,

            _ => Action::None,
        }
    }
}
