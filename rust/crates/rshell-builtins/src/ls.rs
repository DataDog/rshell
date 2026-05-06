//! `ls` — list directory contents. Phase 4 baseline supports `-a`
//! (include hidden), `-l` (long), `-1` (one per line), `-d` (directory
//! itself), `-r` (reverse), `-S` (sort by size), `-t` (sort by mtime).

use rshell_interp::{Builtin, CallCtx};

pub struct Ls;

impl Builtin for Ls {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut all = false;
        let mut long = false;
        let mut one_per_line = false;
        let mut dir_only = false;
        let mut reverse = false;
        let mut sort_size = false;
        let mut sort_mtime = false;
        let mut paths: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--" {
                i += 1;
                while i < ctx.args.len() {
                    paths.push(ctx.args[i].as_slice());
                    i += 1;
                }
                break;
            }
            if a == b"--help" {
                let _ = ctx.stdout.write_all(b"Usage: ls [-aldrSt1] [PATH]...\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b'a' => all = true,
                        b'l' => long = true,
                        b'1' => one_per_line = true,
                        b'd' => dir_only = true,
                        b'r' => reverse = true,
                        b'S' => sort_size = true,
                        b't' => sort_mtime = true,
                        b'A' => all = true, // -A like -a but skips . and ..
                        b'F' | b'h' | b'C' => {}
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
            paths.push(a);
            i += 1;
        }
        if paths.is_empty() {
            paths.push(b".");
        }
        let multi = paths.len() > 1;
        let mut rc = 0;
        for (idx, raw) in paths.iter().enumerate() {
            let path = match std::str::from_utf8(raw) {
                Ok(p) => p,
                Err(_) => continue,
            };
            let metadata = match std::fs::metadata(path) {
                Ok(m) => m,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "ls: {path}: {e}");
                    rc = 1;
                    continue;
                }
            };
            if multi {
                if idx > 0 {
                    let _ = ctx.stdout.write_all(b"\n");
                }
                let _ = writeln!(ctx.stdout, "{path}:");
            }
            if metadata.is_file() || dir_only {
                emit_one(ctx, path, &metadata, long, one_per_line);
                continue;
            }
            let entries = match std::fs::read_dir(path) {
                Ok(d) => d,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "ls: {path}: {e}");
                    rc = 1;
                    continue;
                }
            };
            let mut items: Vec<(String, std::fs::Metadata)> = Vec::new();
            for ent in entries.flatten() {
                let name = ent.file_name().to_string_lossy().into_owned();
                if !all && name.starts_with('.') {
                    continue;
                }
                if let Ok(m) = ent.metadata() {
                    items.push((name, m));
                }
            }
            if sort_size {
                items.sort_by(|a, b| b.1.len().cmp(&a.1.len()));
            } else if sort_mtime {
                items.sort_by(|a, b| {
                    let am = a.1.modified().ok();
                    let bm = b.1.modified().ok();
                    bm.cmp(&am)
                });
            } else {
                items.sort_by(|a, b| a.0.cmp(&b.0));
            }
            if reverse {
                items.reverse();
            }
            for (name, meta) in items {
                emit_one(ctx, &name, &meta, long, one_per_line);
            }
        }
        rc
    }
}

fn emit_one(
    ctx: &mut CallCtx<'_>,
    name: &str,
    meta: &std::fs::Metadata,
    long: bool,
    one_per_line: bool,
) {
    if long {
        let kind = if meta.is_dir() {
            'd'
        } else if meta.file_type().is_symlink() {
            'l'
        } else {
            '-'
        };
        let _ = writeln!(ctx.stdout, "{kind}--------- {:>8} {}", meta.len(), name);
    } else {
        // Phase 4 baseline: emit one per line whether `-1` is set or not.
        let _ = one_per_line;
        let _ = writeln!(ctx.stdout, "{name}");
    }
}
