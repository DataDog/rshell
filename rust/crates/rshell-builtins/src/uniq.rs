//! `uniq` — filter adjacent duplicate lines. Phase 4 baseline supports
//! `-c` (count), `-d` (only duplicates), `-u` (only unique), `-i`
//! (case-insensitive).

use std::io::{Read, Write};

use rshell_interp::{Builtin, CallCtx};

pub struct Uniq;

impl Builtin for Uniq {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut count = false;
        let mut only_dup = false;
        let mut only_uniq = false;
        let mut ignore_case = false;
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
                let _ = ctx.stdout.write_all(b"Usage: uniq [-c] [-d] [-u] [-i] [FILE]\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b'c' => count = true,
                        b'd' => only_dup = true,
                        b'u' => only_uniq = true,
                        b'i' => ignore_case = true,
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
                let _ = writeln!(ctx.stderr, "uniq: {e}");
                return 1;
            }
        } else {
            let path = match std::str::from_utf8(files[0]) {
                Ok(p) => p,
                Err(_) => {
                    let _ = ctx.stderr.write_all(b"uniq: invalid path\n");
                    return 1;
                }
            };
            let res = if path == "-" {
                ctx.stdin.read_to_end(&mut buf)
            } else {
                match std::fs::File::open(path) {
                    Ok(mut f) => f.read_to_end(&mut buf),
                    Err(e) => {
                        let _ = writeln!(ctx.stderr, "uniq: {path}: {e}");
                        return 1;
                    }
                }
            };
            if let Err(e) = res {
                let _ = writeln!(ctx.stderr, "uniq: {e}");
                return 1;
            }
        }
        let mut lines: Vec<&[u8]> = Vec::new();
        let mut start = 0;
        for (i, &b) in buf.iter().enumerate() {
            if b == b'\n' {
                lines.push(&buf[start..i]);
                start = i + 1;
            }
        }
        if start < buf.len() {
            lines.push(&buf[start..]);
        }
        let mut prev: Option<Vec<u8>> = None;
        let mut run = 0u32;
        for line in &lines {
            let key = if ignore_case {
                line.iter().map(|b| b.to_ascii_lowercase()).collect::<Vec<_>>()
            } else {
                line.to_vec()
            };
            match &prev {
                Some(p) if p == &key => run += 1,
                _ => {
                    if let Some(p) = prev.take() {
                        emit(ctx.stdout, &p, run, count, only_dup, only_uniq);
                    }
                    prev = Some(key);
                    run = 1;
                }
            }
        }
        if let Some(p) = prev.take() {
            emit(ctx.stdout, &p, run, count, only_dup, only_uniq);
        }
        0
    }
}

fn emit(dst: &mut dyn Write, line: &[u8], run: u32, count: bool, only_dup: bool, only_uniq: bool) {
    if only_dup && run < 2 {
        return;
    }
    if only_uniq && run > 1 {
        return;
    }
    if count {
        let _ = write!(dst, "{:7} ", run);
    }
    let _ = dst.write_all(line);
    let _ = dst.write_all(b"\n");
}
