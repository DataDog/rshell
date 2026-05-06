//! `rshell-analysis` — runs the symbol-allowlist checks against the
//! workspace and exits 0 on success, 1 on any violation.

use std::path::PathBuf;

fn main() {
    let workspace_root = std::env::var("RSHELL_WORKSPACE_ROOT")
        .map(PathBuf::from)
        .unwrap_or_else(|_| {
            // Walk upwards looking for the workspace Cargo.toml.
            let mut cur = std::env::current_dir().expect("cwd");
            loop {
                if cur.join("Cargo.toml").exists() && cur.join("crates").exists() {
                    return cur;
                }
                if !cur.pop() {
                    panic!("could not locate workspace root");
                }
            }
        });

    let violations = rshell_analysis::analyze(&workspace_root);
    if violations.is_empty() {
        println!("rshell-analysis: 0 violations");
        std::process::exit(0);
    }
    eprintln!("rshell-analysis: {} violation(s):", violations.len());
    for v in &violations {
        eprintln!(
            "  {}:{} [{}]: {}",
            v.file.display(),
            v.line,
            v.crate_name,
            v.message
        );
    }
    std::process::exit(1);
}
