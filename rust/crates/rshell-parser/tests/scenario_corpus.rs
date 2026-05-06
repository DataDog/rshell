//! Phase 2 exit-gate test: every `input.script` in `tests/scenarios/`
//! parses without error.
//!
//! Failure mode: this test prints a coverage summary and the first 10
//! parse errors, then asserts 100% success. While the parser is still
//! catching up, set the env var `RSHELL_PARSER_ALLOW_FAILURES=1` to
//! print the summary without failing.

use std::path::{Path, PathBuf};

use rshell_parser::parse_script;
use serde::Deserialize;

#[derive(Default, Deserialize)]
struct Scenario {
    #[serde(default)]
    input: Input,
}

#[derive(Default, Deserialize)]
struct Input {
    #[serde(default)]
    script: String,
}

fn scenarios_dir() -> PathBuf {
    // The crate lives at rust/crates/rshell-parser; scenarios are at
    // ../../../tests/scenarios relative to the crate root.
    let crate_root = Path::new(env!("CARGO_MANIFEST_DIR"));
    crate_root
        .join("..")
        .join("..")
        .join("..")
        .join("tests")
        .join("scenarios")
}

#[test]
fn round_trips_every_script() {
    let dir = scenarios_dir();
    if !dir.exists() {
        eprintln!(
            "scenarios directory not found at {}; skipping",
            dir.display()
        );
        return;
    }
    let allow_failures = std::env::var_os("RSHELL_PARSER_ALLOW_FAILURES").is_some();

    let mut total = 0usize;
    let mut ok = 0usize;
    let mut failures: Vec<(PathBuf, String)> = Vec::new();

    for entry in walkdir::WalkDir::new(&dir) {
        let entry = entry.expect("walk");
        if !entry.file_type().is_file() {
            continue;
        }
        let path = entry.path();
        match path.extension().and_then(|s| s.to_str()) {
            Some("yaml" | "yml") => {}
            _ => continue,
        }
        let text = std::fs::read_to_string(path).expect("read");
        let scenario: Scenario = match serde_yaml_ng::from_str(&text) {
            Ok(s) => s,
            Err(_) => continue, // skip unparseable yaml — runner crate covers it
        };
        if scenario.input.script.is_empty() {
            continue;
        }
        total += 1;
        match parse_script(scenario.input.script.as_bytes()) {
            Ok(_) => ok += 1,
            Err(e) => failures.push((path.to_path_buf(), format!("{e}"))),
        }
    }

    let failed = failures.len();
    let pct = (ok * 10000)
        .checked_div(total)
        .map(|v| v as f64 / 100.0)
        .unwrap_or(0.0);
    eprintln!("\nPhase 2 corpus coverage: {ok}/{total} ({pct:.2}%)  failures={failed}");

    if failed > 0 {
        eprintln!("\nFirst {} failures:", failures.len().min(10));
        for (p, e) in failures.iter().take(10) {
            eprintln!("  {}\n    {e}", p.display());
        }
    }

    if !allow_failures {
        assert_eq!(failed, 0, "{failed} script(s) failed to parse — see stderr");
    }
}
