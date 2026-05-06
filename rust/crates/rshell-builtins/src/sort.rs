//! `sort` — sort lines. Phase 4 baseline supports `-r` (reverse), `-n`
//! (numeric), `-u` (unique), `-f` (case-insensitive), and reads from
//! stdin or a list of files.

use std::io::Read;

use rshell_interp::{Builtin, CallCtx};

pub struct Sort;

impl Builtin for Sort {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut reverse = false;
        let mut numeric = false;
        let mut unique = false;
        let mut fold = false;
        let mut files: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--" {
                i += 1;
                while i < ctx.args.len() {
                    files.push(ctx.args[i].as_slice());
                    i += 1;
                }
                break;
            }
            if a == b"--help" {
                let _ = ctx.stdout.write_all(b"Usage: sort [-r] [-n] [-u] [-f] [FILE]...\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b'r' => reverse = true,
                        b'n' => numeric = true,
                        b'u' => unique = true,
                        b'f' => fold = true,
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
            files.push(a);
            i += 1;
        }
        let mut buf = Vec::<u8>::new();
        if files.is_empty() {
            if let Err(e) = ctx.stdin.read_to_end(&mut buf) {
                let _ = writeln!(ctx.stderr, "sort: {e}");
                return 1;
            }
        } else {
            for f in files {
                let path = match std::str::from_utf8(f) {
                    Ok(p) => p,
                    Err(_) => {
                        let _ = ctx.stderr.write_all(b"sort: invalid path\n");
                        return 1;
                    }
                };
                let res = if path == "-" {
                    ctx.stdin.read_to_end(&mut buf)
                } else {
                    match std::fs::File::open(path) {
                        Ok(mut f) => f.read_to_end(&mut buf),
                        Err(e) => {
                            let _ = writeln!(ctx.stderr, "sort: {path}: {e}");
                            return 1;
                        }
                    }
                };
                if let Err(e) = res {
                    let _ = writeln!(ctx.stderr, "sort: {e}");
                    return 1;
                }
            }
        }
        let mut lines: Vec<Vec<u8>> = Vec::new();
        let mut start = 0;
        for (i, &b) in buf.iter().enumerate() {
            if b == b'\n' {
                lines.push(buf[start..i].to_vec());
                start = i + 1;
            }
        }
        if start < buf.len() {
            lines.push(buf[start..].to_vec());
        }
        if numeric {
            lines.sort_by(|a, b| {
                let na = parse_leading_number(a);
                let nb = parse_leading_number(b);
                na.partial_cmp(&nb).unwrap_or(std::cmp::Ordering::Equal)
            });
        } else if fold {
            lines.sort_by(|a, b| fold_cmp(a, b));
        } else {
            lines.sort();
        }
        if reverse {
            lines.reverse();
        }
        if unique {
            lines.dedup();
        }
        for line in lines {
            let _ = ctx.stdout.write_all(&line);
            let _ = ctx.stdout.write_all(b"\n");
        }
        0
    }
}

fn parse_leading_number(s: &[u8]) -> f64 {
    let s = std::str::from_utf8(s).unwrap_or("0");
    let trimmed = s.trim_start();
    let mut end = 0;
    let bytes = trimmed.as_bytes();
    if !bytes.is_empty() && (bytes[0] == b'-' || bytes[0] == b'+') {
        end = 1;
    }
    while end < bytes.len() && (bytes[end].is_ascii_digit() || bytes[end] == b'.') {
        end += 1;
    }
    trimmed[..end].parse::<f64>().unwrap_or(0.0)
}

fn fold_cmp(a: &[u8], b: &[u8]) -> std::cmp::Ordering {
    for (x, y) in a.iter().zip(b.iter()) {
        let xl = x.to_ascii_lowercase();
        let yl = y.to_ascii_lowercase();
        match xl.cmp(&yl) {
            std::cmp::Ordering::Equal => continue,
            o => return o,
        }
    }
    a.len().cmp(&b.len())
}
