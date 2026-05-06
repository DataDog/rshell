//! `grep` — search for regex matches. Phase 4 baseline supports `-i`,
//! `-v`, `-c`, `-l`, `-n`, `-E` (extended regex by default in Rust),
//! `-F` (fixed strings), `-q` (quiet).

use rshell_interp::{Builtin, CallCtx};

pub struct Grep;

impl Builtin for Grep {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut ignore_case = false;
        let mut invert = false;
        let mut count_only = false;
        let mut filenames_only = false;
        let mut show_line_no = false;
        let mut quiet = false;
        let mut fixed = false;
        let mut pattern: Option<&[u8]> = None;
        let mut files: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--" {
                i += 1;
                if pattern.is_none() && i < ctx.args.len() {
                    pattern = Some(ctx.args[i].as_slice());
                    i += 1;
                }
                while i < ctx.args.len() {
                    files.push(ctx.args[i].as_slice());
                    i += 1;
                }
                break;
            }
            if a == b"--help" {
                let _ = ctx.stdout.write_all(
                    b"Usage: grep [-i] [-v] [-c] [-l] [-n] [-E] [-F] [-q] PATTERN [FILE]...\n",
                );
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b'i' => ignore_case = true,
                        b'v' => invert = true,
                        b'c' => count_only = true,
                        b'l' => filenames_only = true,
                        b'n' => show_line_no = true,
                        b'q' => quiet = true,
                        b'E' => {} // ERE is the default for the `regex` crate
                        b'F' => fixed = true,
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
            if pattern.is_none() {
                pattern = Some(a);
            } else {
                files.push(a);
            }
            i += 1;
        }
        let Some(pat_bytes) = pattern else {
            let _ = ctx.stderr.write_all(b"grep: missing pattern\n");
            return 2;
        };
        let pat = match std::str::from_utf8(pat_bytes) {
            Ok(s) => s,
            Err(_) => {
                let _ = ctx.stderr.write_all(b"grep: pattern must be UTF-8\n");
                return 2;
            }
        };
        let pat = if fixed {
            regex::escape(pat)
        } else {
            pat.to_string()
        };
        let mut builder = regex::bytes::RegexBuilder::new(&pat);
        builder.case_insensitive(ignore_case);
        let regex = match builder.build() {
            Ok(r) => r,
            Err(e) => {
                let _ = writeln!(ctx.stderr, "grep: bad regex: {e}");
                return 2;
            }
        };

        let mut overall_match = false;
        let multi = files.len() > 1;
        let process = |buf: &[u8], path: Option<&str>, ctx: &mut CallCtx<'_>| -> bool {
            let mut count = 0usize;
            let mut any = false;
            let mut line_no = 0usize;
            let mut start = 0;
            for i in 0..=buf.len() {
                if i == buf.len() || buf[i] == b'\n' {
                    let line = &buf[start..i];
                    line_no += 1;
                    let m = regex.is_match(line);
                    let hit = if invert { !m } else { m };
                    if hit {
                        any = true;
                        count += 1;
                        if !quiet && !count_only && !filenames_only {
                            if multi && let Some(p) = path {
                                let _ = ctx.stdout.write_all(p.as_bytes());
                                let _ = ctx.stdout.write_all(b":");
                            }
                            if show_line_no {
                                let _ = write!(ctx.stdout, "{line_no}:");
                            }
                            let _ = ctx.stdout.write_all(line);
                            let _ = ctx.stdout.write_all(b"\n");
                        }
                    }
                    start = i + 1;
                    if i == buf.len() {
                        break;
                    }
                }
            }
            if filenames_only
                && any
                && let Some(p) = path
            {
                let _ = ctx.stdout.write_all(p.as_bytes());
                let _ = ctx.stdout.write_all(b"\n");
            }
            if count_only {
                if multi && let Some(p) = path {
                    let _ = ctx.stdout.write_all(p.as_bytes());
                    let _ = ctx.stdout.write_all(b":");
                }
                let _ = writeln!(ctx.stdout, "{count}");
            }
            any
        };

        if files.is_empty() {
            let mut buf = Vec::new();
            let _ = ctx.stdin.read_to_end(&mut buf);
            overall_match = process(&buf, None, ctx);
        } else {
            for f in files {
                let path = match std::str::from_utf8(f) {
                    Ok(p) => p,
                    Err(_) => continue,
                };
                let buf = if path == "-" {
                    let mut b = Vec::new();
                    let _ = ctx.stdin.read_to_end(&mut b);
                    b
                } else {
                    match std::fs::read(path) {
                        Ok(b) => b,
                        Err(e) => {
                            let _ = writeln!(ctx.stderr, "grep: {path}: {e}");
                            continue;
                        }
                    }
                };
                if process(&buf, Some(path), ctx) {
                    overall_match = true;
                }
            }
        }
        if overall_match { 0 } else { 1 }
    }
}
