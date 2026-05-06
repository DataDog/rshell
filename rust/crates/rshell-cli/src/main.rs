//! `rshell-rs` — Rust port of the rshell binary. Phase 0 scaffolding.

use clap::Parser;

#[derive(Debug, Parser)]
#[command(
    name = "rshell-rs",
    version,
    about = "Restricted shell interpreter (Rust port — work in progress)"
)]
struct Cli {
    /// Run a single command string instead of a script file.
    #[arg(short = 'c', value_name = "COMMAND")]
    command: Option<String>,

    /// Allowed filesystem path (repeatable).
    #[arg(long = "allowed-path", value_name = "PATH")]
    allowed_paths: Vec<String>,

    /// Script file to execute, followed by its arguments.
    #[arg(value_name = "SCRIPT")]
    script: Option<String>,
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();
    if cli.command.is_some() || cli.script.is_some() || !cli.allowed_paths.is_empty() {
        anyhow::bail!(
            "rshell-rs is not yet implemented; see rust/PROGRESS.md for the migration plan"
        );
    }
    println!("rshell-rs {} (scaffolding only)", env!("CARGO_PKG_VERSION"));
    Ok(())
}
