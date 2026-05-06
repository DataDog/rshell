//! `cat` — concatenate files (or stdin) to stdout.
//!
//! Phase 3 baseline supports stream-through with no flags. The full GNU
//! `cat` flag set (-n, -b, -A, -E, -T, -s, -v) lands in Phase 4 alongside
//! the rest of the file-reader builtins.

use std::io::{Read, Write};

use rshell_interp::{Builtin, CallCtx};

pub struct Cat;

impl Builtin for Cat {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        if ctx.args.len() <= 1 {
            // No file args: stream stdin.
            return copy(ctx.stdin, ctx.stdout, ctx.stderr);
        }
        for a in &ctx.args[1..] {
            let path = match std::str::from_utf8(a.as_slice()) {
                Ok(p) => p,
                Err(_) => {
                    let _ = writeln!(ctx.stderr, "cat: invalid path");
                    return 1;
                }
            };
            if path == "-" {
                let code = copy(ctx.stdin, ctx.stdout, ctx.stderr);
                if code != 0 {
                    return code;
                }
                continue;
            }
            let mut f = match std::fs::File::open(path) {
                Ok(f) => f,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "cat: {path}: {e}");
                    return 1;
                }
            };
            let code = copy(&mut f, ctx.stdout, ctx.stderr);
            if code != 0 {
                return code;
            }
        }
        0
    }
}

fn copy(src: &mut dyn Read, dst: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let mut buf = [0u8; 32 * 1024];
    loop {
        match src.read(&mut buf) {
            Ok(0) => return 0,
            Ok(n) => {
                if let Err(e) = dst.write_all(&buf[..n]) {
                    let _ = writeln!(stderr, "cat: {e}");
                    return 1;
                }
            }
            Err(e) => {
                let _ = writeln!(stderr, "cat: {e}");
                return 1;
            }
        }
    }
}
