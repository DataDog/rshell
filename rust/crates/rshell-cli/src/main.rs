//! `rshell-rs` — the Rust restricted shell binary. Phase 3 baseline.

use std::io::Read;
use std::path::PathBuf;

use clap::Parser;
use rshell_interp::{BuiltinRegistry, Env, Runner, run_script};
use rshell_parser::parse_script;

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

    /// Comma-separated list of allowed paths (currently advisory; the
    /// sandbox layer lands in a follow-up phase).
    #[arg(short = 'p', long = "allowed-paths", value_name = "PATHS")]
    allowed_paths: Option<String>,

    /// Comma-separated list of allowed command names. Currently advisory.
    #[arg(long = "allowed-commands", value_name = "CMDS")]
    allowed_commands: Option<String>,

    /// Allow execution of all commands. Currently the default; included
    /// for CLI parity with the Go binary.
    #[arg(long = "allow-all-commands")]
    allow_all_commands: bool,

    /// Script file to execute, followed by its arguments.
    #[arg(value_name = "SCRIPT")]
    script: Option<String>,

    #[arg(trailing_var_arg = true)]
    rest: Vec<String>,
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();

    let script_text: Vec<u8> = if let Some(c) = cli.command.as_deref() {
        c.as_bytes().to_vec()
    } else if let Some(path) = cli.script.as_deref() {
        std::fs::read(path)?
    } else {
        let mut buf = Vec::new();
        std::io::stdin().read_to_end(&mut buf)?;
        buf
    };

    let parsed = match parse_script(&script_text) {
        Ok(s) => s,
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(2);
        }
    };

    let mut builtins = BuiltinRegistry::new();
    rshell_builtins::register_all(&mut builtins);

    let env = Env::new();
    let cwd = if let Some(p) = cli
        .allowed_paths
        .as_deref()
        .and_then(|s| s.split(',').next())
    {
        PathBuf::from(p)
    } else {
        std::env::current_dir().unwrap_or_else(|_| PathBuf::from("/"))
    };
    // Best-effort: change actual process CWD so subprocesses (none today,
    // but future builtins) start in the right place.
    let _ = std::env::set_current_dir(&cwd);

    let mut runner = Runner::new(builtins, env, cwd);
    let mut stdin = std::io::stdin().lock();
    let mut stdout = std::io::stdout().lock();
    let mut stderr = std::io::stderr().lock();
    let code = run_script(&mut runner, &parsed, &mut stdin, &mut stdout, &mut stderr);
    std::process::exit(code);
}
