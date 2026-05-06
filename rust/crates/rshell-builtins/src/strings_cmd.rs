//! `strings` — print printable runs of ≥N bytes from the input. Phase 4
//! baseline supports `-n N` and reading from files or stdin.

use rshell_interp::{Builtin, CallCtx};

pub struct Strings;

impl Builtin for Strings {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut min_len = 4usize;
        let mut files: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--help" {
                let _ = ctx.stdout.write_all(b"Usage: strings [-n N] [FILE]...\n");
                return 0;
            }
            if let Some(rest) = a.strip_prefix(b"-n") {
                let val: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"strings: option requires an argument -- 'n'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                min_len = std::str::from_utf8(val).unwrap_or("4").parse().unwrap_or(4);
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        let mut buf = Vec::new();
        if files.is_empty() {
            let _ = ctx.stdin.read_to_end(&mut buf);
        } else {
            for f in files {
                let path = match std::str::from_utf8(f) {
                    Ok(p) => p,
                    Err(_) => continue,
                };
                if path == "-" {
                    let _ = ctx.stdin.read_to_end(&mut buf);
                } else if let Ok(b) = std::fs::read(path) {
                    buf.extend_from_slice(&b);
                }
            }
        }
        let mut current = Vec::<u8>::new();
        for &b in &buf {
            if (32..127).contains(&b) || b == b'\t' {
                current.push(b);
            } else {
                if current.len() >= min_len {
                    let _ = ctx.stdout.write_all(&current);
                    let _ = ctx.stdout.write_all(b"\n");
                }
                current.clear();
            }
        }
        if current.len() >= min_len {
            let _ = ctx.stdout.write_all(&current);
            let _ = ctx.stdout.write_all(b"\n");
        }
        0
    }
}
