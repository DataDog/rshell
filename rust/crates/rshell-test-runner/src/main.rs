//! `rshell-test-runner` — drives YAML scenarios against an external
//! `rshell`-compatible binary. Phase 1 of the Rust port.

use std::path::PathBuf;
use std::time::{Duration, Instant};

use clap::Parser;
use rshell_test_runner::scenario::Scenario;
use rshell_test_runner::{Outcome, RunOptions, run_scenario};

#[derive(Debug, Parser)]
#[command(
    name = "rshell-test-runner",
    version,
    about = "Run YAML scenario tests against an rshell-compatible binary"
)]
struct Cli {
    /// Path to the rshell binary to drive (default: `rshell` on $PATH).
    #[arg(long, default_value = "rshell")]
    bin: PathBuf,
    /// Per-scenario subprocess timeout in seconds.
    #[arg(long, default_value_t = 60)]
    timeout_secs: u64,
    /// Substring filter — only run scenarios whose path contains this string.
    #[arg(long)]
    filter: Option<String>,
    /// Path to a file listing scenario paths (relative to scenarios_dir),
    /// one per line. Lines starting with `#` and blank lines are ignored.
    /// Mutually exclusive with --filter.
    #[arg(long)]
    filter_list: Option<PathBuf>,
    /// Stop after the first failure.
    #[arg(long)]
    fail_fast: bool,
    /// Directory of YAML scenarios to walk.
    scenarios_dir: PathBuf,
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();
    let opts = RunOptions {
        binary: cli.bin,
        timeout: Duration::from_secs(cli.timeout_secs),
    };

    let allow_list: Option<std::collections::HashSet<PathBuf>> =
        if let Some(p) = cli.filter_list.as_deref() {
            let text = std::fs::read_to_string(p)
                .map_err(|e| anyhow::anyhow!("read {}: {e}", p.display()))?;
            let mut set = std::collections::HashSet::new();
            for line in text.lines() {
                let line = line.trim();
                if line.is_empty() || line.starts_with('#') {
                    continue;
                }
                set.insert(cli.scenarios_dir.join(line));
            }
            Some(set)
        } else {
            None
        };
    let scenarios = collect_scenarios(
        &cli.scenarios_dir,
        cli.filter.as_deref(),
        allow_list.as_ref(),
    )?;
    if scenarios.is_empty() {
        anyhow::bail!("no scenarios found under {:?}", cli.scenarios_dir);
    }

    let mut passed = 0usize;
    let mut failed = 0usize;
    let mut skipped = 0usize;
    let mut failures: Vec<(PathBuf, String)> = Vec::new();
    let started = Instant::now();

    for (path, scenario) in &scenarios {
        let rel = path
            .strip_prefix(&cli.scenarios_dir)
            .unwrap_or(path)
            .display()
            .to_string();
        match run_scenario(scenario, &opts) {
            Outcome::Passed => {
                passed += 1;
                println!("PASS  {rel}");
            }
            Outcome::Skipped(reason) => {
                skipped += 1;
                println!("SKIP  {rel}  ({})", reason.as_str());
            }
            Outcome::Failed(err) => {
                failed += 1;
                let msg = err.to_string();
                println!("FAIL  {rel}\n      {msg}");
                failures.push((path.clone(), msg));
                if cli.fail_fast {
                    break;
                }
            }
        }
    }

    let elapsed = started.elapsed();
    println!(
        "\nsummary: {passed} passed, {failed} failed, {skipped} skipped, {} total in {:.2?}",
        scenarios.len(),
        elapsed,
    );

    if failed > 0 {
        std::process::exit(1)
    } else {
        Ok(())
    }
}

fn collect_scenarios(
    root: &std::path::Path,
    filter: Option<&str>,
    allow_list: Option<&std::collections::HashSet<PathBuf>>,
) -> anyhow::Result<Vec<(PathBuf, Scenario)>> {
    let mut out = Vec::new();
    for entry in walkdir::WalkDir::new(root) {
        let entry = entry?;
        if !entry.file_type().is_file() {
            continue;
        }
        let path = entry.path();
        match path.extension().and_then(|s| s.to_str()) {
            Some("yaml" | "yml") => {}
            _ => continue,
        }
        let path_str = path.to_string_lossy();
        if let Some(needle) = filter
            && !path_str.contains(needle)
        {
            continue;
        }
        if let Some(set) = allow_list
            && !set.contains(path)
        {
            continue;
        }
        let text = std::fs::read_to_string(path)
            .map_err(|e| anyhow::anyhow!("read {}: {e}", path.display()))?;
        let scenario = Scenario::from_yaml(&text)
            .map_err(|e| anyhow::anyhow!("parse {}: {e}", path.display()))?;
        out.push((path.to_path_buf(), scenario));
    }
    out.sort_by(|a, b| a.0.cmp(&b.0));
    Ok(out)
}
