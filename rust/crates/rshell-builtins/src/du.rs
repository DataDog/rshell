//! `du` — disk usage. Phase 4 baseline supports `-s` (summarise), `-h`
//! (human-readable), `-a` (all files), and per-path totals.

use rshell_interp::{Builtin, CallCtx};

pub struct Du;

impl Builtin for Du {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut summarize = false;
        let mut human = false;
        let mut all = false;
        let mut paths: Vec<String> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--help" {
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: du [-s] [-h] [-a] [PATH]...\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b's' => summarize = true,
                        b'h' => human = true,
                        b'a' => all = true,
                        _ => {
                            ok = false;
                            break;
                        }
                    }
                }
                if ok {
                    i += 1;
                    continue;
                }
            }
            paths.push(String::from_utf8_lossy(a).into_owned());
            i += 1;
        }
        if paths.is_empty() {
            paths.push(".".into());
        }
        let mut rc = 0;
        for path in paths {
            match measure(&path, all, summarize) {
                Ok(items) => {
                    for (n, p) in items {
                        let size = if human {
                            human_size(n)
                        } else {
                            format!("{}", n.div_ceil(1024))
                        };
                        let _ = writeln!(ctx.stdout, "{size}\t{p}");
                    }
                }
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "du: {path}: {e}");
                    rc = 1;
                }
            }
        }
        rc
    }
}

fn measure(root: &str, all: bool, summarize: bool) -> std::io::Result<Vec<(u64, String)>> {
    let mut out = Vec::new();
    let total = walk(root, all, summarize, &mut out)?;
    if summarize || out.is_empty() {
        out.clear();
        out.push((total, root.to_string()));
    } else {
        out.push((total, root.to_string()));
    }
    Ok(out)
}

fn walk(
    path: &str,
    all: bool,
    summarize: bool,
    out: &mut Vec<(u64, String)>,
) -> std::io::Result<u64> {
    let metadata = std::fs::metadata(path)?;
    if metadata.is_file() {
        let bytes = metadata.len();
        if all && !summarize {
            out.push((bytes, path.to_string()));
        }
        return Ok(bytes);
    }
    let mut total = 0u64;
    if let Ok(entries) = std::fs::read_dir(path) {
        for ent in entries.flatten() {
            let p = ent.path().to_string_lossy().into_owned();
            total += walk(&p, all, summarize, out)?;
        }
    }
    if !summarize {
        out.push((total, path.to_string()));
    }
    Ok(total)
}

fn human_size(bytes: u64) -> String {
    const UNITS: &[&str] = &["B", "K", "M", "G", "T"];
    let mut n = bytes as f64;
    let mut idx = 0;
    while n >= 1024.0 && idx + 1 < UNITS.len() {
        n /= 1024.0;
        idx += 1;
    }
    if idx == 0 {
        format!("{}{}", bytes, UNITS[0])
    } else {
        format!("{:.1}{}", n, UNITS[idx])
    }
}
