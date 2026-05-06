//! `tail` — output the last N lines (default 10) of files.

use std::io::{Read, Write};

use rshell_interp::{Builtin, CallCtx};

pub struct Tail;

impl Builtin for Tail {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut lines: Option<usize> = None;
        let mut bytes: Option<usize> = None;
        let mut from_start = false;
        let mut quiet = false;
        let mut verbose = false;
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
                let _ = ctx.stdout.write_all(b"Usage: tail [-n NUM] [-c NUM] [-q] [-v] [FILE]...\n");
                return 0;
            }
            if let Some(rest) = a.strip_prefix(b"--lines=") {
                let (start, n) = parse_lines_arg(rest);
                from_start = start;
                lines = Some(n);
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"--bytes=") {
                let (start, n) = parse_lines_arg(rest);
                from_start = start;
                bytes = Some(n);
                i += 1;
                continue;
            }
            if a == b"--quiet" || a == b"--silent" {
                quiet = true;
                i += 1;
                continue;
            }
            if a == b"--verbose" {
                verbose = true;
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-n") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx.stderr.write_all(b"tail: option requires an argument -- 'n'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                let (start, n) = parse_lines_arg(value);
                from_start = start;
                lines = Some(n);
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-c") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx.stderr.write_all(b"tail: option requires an argument -- 'c'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                let (start, n) = parse_lines_arg(value);
                from_start = start;
                bytes = Some(n);
                i += 1;
                continue;
            }
            if a == b"-q" {
                quiet = true;
                i += 1;
                continue;
            }
            if a == b"-v" {
                verbose = true;
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        let n_lines = lines.unwrap_or(10);
        let show_headers = (verbose || files.len() > 1) && !quiet;

        if files.is_empty() {
            let buf = match read_all(ctx.stdin) {
                Ok(b) => b,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "tail: {e}");
                    return 1;
                }
            };
            tail_bytes(ctx.stdout, &buf, n_lines, bytes, from_start);
            return 0;
        }
        let mut rc = 0;
        for (idx, fpath) in files.iter().enumerate() {
            let path = match std::str::from_utf8(fpath) {
                Ok(p) => p,
                Err(_) => {
                    let _ = ctx.stderr.write_all(b"tail: invalid path\n");
                    rc = 1;
                    continue;
                }
            };
            if show_headers {
                if idx > 0 {
                    let _ = ctx.stdout.write_all(b"\n");
                }
                let _ = ctx.stdout.write_all(b"==> ");
                let _ = ctx.stdout.write_all(path.as_bytes());
                let _ = ctx.stdout.write_all(b" <==\n");
            }
            let buf = if path == "-" {
                read_all(ctx.stdin)
            } else {
                match std::fs::File::open(path) {
                    Ok(mut f) => read_all(&mut f),
                    Err(e) => {
                        let _ = writeln!(ctx.stderr, "tail: {path}: {e}");
                        rc = 1;
                        continue;
                    }
                }
            };
            let buf = match buf {
                Ok(b) => b,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "tail: {path}: {e}");
                    rc = 1;
                    continue;
                }
            };
            tail_bytes(ctx.stdout, &buf, n_lines, bytes, from_start);
        }
        rc
    }
}

fn parse_lines_arg(s: &[u8]) -> (bool, usize) {
    let mut from_start = false;
    let s = if let Some(rest) = s.strip_prefix(b"+") {
        from_start = true;
        rest
    } else {
        s
    };
    let s = std::str::from_utf8(s).unwrap_or("0");
    let n = s.trim().parse::<i64>().unwrap_or(0);
    (from_start, n.unsigned_abs() as usize)
}

fn read_all(src: &mut dyn Read) -> std::io::Result<Vec<u8>> {
    let mut v = Vec::new();
    src.read_to_end(&mut v)?;
    Ok(v)
}

fn tail_bytes(dst: &mut dyn Write, buf: &[u8], lines: usize, bytes: Option<usize>, from_start: bool) {
    if let Some(n) = bytes {
        if from_start {
            let start = n.saturating_sub(1).min(buf.len());
            let _ = dst.write_all(&buf[start..]);
        } else {
            let start = buf.len().saturating_sub(n);
            let _ = dst.write_all(&buf[start..]);
        }
        return;
    }
    if from_start {
        // Print starting from the Nth line (1-indexed).
        let mut count = 1;
        let mut i = 0;
        while i < buf.len() && count < lines {
            if buf[i] == b'\n' {
                count += 1;
            }
            i += 1;
        }
        let _ = dst.write_all(&buf[i..]);
        return;
    }
    // Last `lines` lines.
    let mut newline_indices = Vec::new();
    for (i, &b) in buf.iter().enumerate() {
        if b == b'\n' {
            newline_indices.push(i);
        }
    }
    let total_lines = if buf.last() == Some(&b'\n') {
        newline_indices.len()
    } else {
        newline_indices.len() + 1
    };
    if lines >= total_lines {
        let _ = dst.write_all(buf);
        return;
    }
    let skip = total_lines - lines;
    let start = if skip == 0 {
        0
    } else if skip <= newline_indices.len() {
        newline_indices[skip - 1] + 1
    } else {
        buf.len()
    };
    let _ = dst.write_all(&buf[start..]);
}
