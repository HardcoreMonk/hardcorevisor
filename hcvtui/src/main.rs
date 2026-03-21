//! # hcvtui — HardCoreVisor Terminal UI
//!
//! Ratatui + Crossterm based management console.
//! Connects to Go Controller via REST API.

mod api_client;
mod app;
mod keybindings;
mod ui;

use color_eyre::Result;

#[tokio::main]
async fn main() -> Result<()> {
    color_eyre::install()?;
    tracing_subscriber::fmt()
        .with_env_filter("hcvtui=debug")
        .init();

    let mut terminal = ratatui::init();
    let result = app::App::new().run(&mut terminal).await;
    ratatui::restore();
    result
}
