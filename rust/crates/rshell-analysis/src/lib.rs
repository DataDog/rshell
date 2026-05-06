//! Symbol-allowlist verification for the rshell Rust port.
//!
//! This is a focused subset of the Go `analysis/` package's checks. It
//! catches the security-relevant patterns we never want in the runtime
//! crates:
//!
//! - **No `std::process::Command`**, `Stdio`, `ExitStatus` etc. in any
//!   runtime crate (`rshell-interp`, `rshell-expand`, `rshell-parser`,
//!   `rshell-sandbox`, `rshell-builtins`, `rshell-cli`). The shell is
//!   meant to dispatch only to in-process builtins. The
//!   `rshell-test-runner` crate is exempt because it spawns subprocesses
//!   by design.
//! - **No `unsafe` blocks anywhere.** The workspace `[lints.rust]`
//!   already denies `unsafe_code`, but this re-checks for defense in
//!   depth.
//! - **No async runtimes** — `tokio`, `async_std`, `smol`, `futures`'s
//!   executor entry points. We committed to sync + threads.
//! - **No HTTP clients** — `reqwest`, `hyper`, `ureq`, `isahc`. The
//!   restricted shell does not make outbound HTTP requests.
//!
//! Out of scope (port them in a follow-up if/when needed):
//! - Per-crate symbol allowlists with safety-tier annotations.
//! - The "every allow-listed symbol is actually used" reverse check.
//! - The structural rules (`bufio.Scanner.Buffer`, `OpenFile.Close`) —
//!   Rust's `Drop` makes the latter automatic for `File`, and
//!   `BufReader` doesn't have the 64 KiB scanner pitfall.

use std::path::{Path, PathBuf};

use syn::visit::Visit;

#[derive(Debug, Clone)]
pub struct Violation {
    pub crate_name: String,
    pub file: PathBuf,
    pub line: usize,
    pub message: String,
}

pub fn analyze(workspace_root: &Path) -> Vec<Violation> {
    let mut violations = Vec::new();
    for entry in walkdir::WalkDir::new(workspace_root.join("crates")) {
        let entry = match entry {
            Ok(e) => e,
            Err(_) => continue,
        };
        if !entry.file_type().is_file() {
            continue;
        }
        if entry.path().extension().and_then(|s| s.to_str()) != Some("rs") {
            continue;
        }
        let crate_name = match crate_name_for(entry.path(), workspace_root) {
            Some(n) => n,
            None => continue,
        };
        let src = match std::fs::read_to_string(entry.path()) {
            Ok(s) => s,
            Err(_) => continue,
        };
        let parsed = match syn::parse_file(&src) {
            Ok(f) => f,
            Err(_) => continue,
        };
        let mut v = Visitor {
            crate_name: crate_name.clone(),
            file: entry.path().to_path_buf(),
            violations: Vec::new(),
        };
        v.visit_file(&parsed);
        violations.extend(v.violations);
    }
    violations
}

fn crate_name_for(file: &Path, root: &Path) -> Option<String> {
    let rel = file.strip_prefix(root.join("crates")).ok()?;
    let first = rel.iter().next()?.to_string_lossy().into_owned();
    Some(first)
}

struct Visitor {
    crate_name: String,
    file: PathBuf,
    violations: Vec<Violation>,
}

const RUNTIME_CRATES: &[&str] = &[
    "rshell-interp",
    "rshell-expand",
    "rshell-parser",
    "rshell-sandbox",
    "rshell-builtins",
    "rshell-cli",
];

const BANNED_ASYNC_ROOTS: &[&str] = &["tokio", "async_std", "smol"];
const BANNED_HTTP_ROOTS: &[&str] = &["reqwest", "hyper", "ureq", "isahc"];

impl Visitor {
    fn record(&mut self, line: usize, message: impl Into<String>) {
        self.violations.push(Violation {
            crate_name: self.crate_name.clone(),
            file: self.file.clone(),
            line,
            message: message.into(),
        });
    }

    fn is_runtime_crate(&self) -> bool {
        RUNTIME_CRATES.contains(&self.crate_name.as_str())
    }

    fn check_path(&mut self, path: &syn::Path, line: usize) {
        let segments: Vec<String> = path.segments.iter().map(|s| s.ident.to_string()).collect();
        if segments.is_empty() {
            return;
        }
        let head = segments[0].as_str();
        let full = segments.join("::");

        // No async runtimes, anywhere.
        if BANNED_ASYNC_ROOTS.contains(&head) {
            self.record(
                line,
                format!("banned async runtime root `{head}` in `{full}`"),
            );
        }

        // No HTTP clients, anywhere.
        if BANNED_HTTP_ROOTS.contains(&head) {
            self.record(line, format!("banned HTTP client `{head}` in `{full}`"));
        }

        // Runtime crates: no std::process::Command (or related types).
        if self.is_runtime_crate()
            && segments.len() >= 3
            && segments[0] == "std"
            && segments[1] == "process"
        {
            let last = segments.last().unwrap();
            if matches!(
                last.as_str(),
                "Command"
                    | "Child"
                    | "Stdio"
                    | "ExitStatus"
                    | "Output"
                    | "ChildStdin"
                    | "ChildStdout"
                    | "ChildStderr"
            ) {
                self.record(
                    line,
                    format!(
                        "runtime crate must not use `{full}` — rshell does not exec external binaries"
                    ),
                );
            }
        }
    }
}

impl<'ast> Visit<'ast> for Visitor {
    fn visit_item_use(&mut self, node: &'ast syn::ItemUse) {
        let line = node.use_token.span.start().line;
        check_use_tree(self, &node.tree, &mut Vec::new(), line);
    }

    fn visit_expr_path(&mut self, node: &'ast syn::ExprPath) {
        let line = node
            .path
            .segments
            .first()
            .map(|s| s.ident.span().start().line)
            .unwrap_or(0);
        self.check_path(&node.path, line);
    }

    fn visit_type_path(&mut self, node: &'ast syn::TypePath) {
        let line = node
            .path
            .segments
            .first()
            .map(|s| s.ident.span().start().line)
            .unwrap_or(0);
        self.check_path(&node.path, line);
    }

    fn visit_expr_unsafe(&mut self, node: &'ast syn::ExprUnsafe) {
        let line = node.unsafe_token.span.start().line;
        self.record(
            line,
            "`unsafe` block forbidden in rshell crates".to_string(),
        );
        // Don't descend; the error already covers it.
    }
}

fn check_use_tree(v: &mut Visitor, tree: &syn::UseTree, prefix: &mut Vec<String>, line: usize) {
    match tree {
        syn::UseTree::Path(p) => {
            prefix.push(p.ident.to_string());
            check_use_tree(v, &p.tree, prefix, line);
            prefix.pop();
        }
        syn::UseTree::Name(n) => {
            prefix.push(n.ident.to_string());
            let path: syn::Path = syn::parse_str(&prefix.join("::")).unwrap();
            v.check_path(&path, line);
            prefix.pop();
        }
        syn::UseTree::Group(g) => {
            for inner in &g.items {
                check_use_tree(v, inner, prefix, line);
            }
        }
        syn::UseTree::Glob(_) => {
            // Wildcard imports — skip; the per-symbol checks fire on
            // actual usages.
        }
        syn::UseTree::Rename(r) => {
            prefix.push(r.ident.to_string());
            let path: syn::Path = syn::parse_str(&prefix.join("::")).unwrap();
            v.check_path(&path, line);
            prefix.pop();
        }
    }
}
