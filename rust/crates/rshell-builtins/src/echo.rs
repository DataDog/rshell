//! `echo` — print arguments separated by spaces, terminated by a newline.
//!
//! Flags: `-n` (no trailing newline), `-e` (interpret backslash escapes),
//! `-E` (disable escapes). Bash convention: leading argv elements that
//! match `-[neE]+` are treated as flags; the first non-flag stops flag
//! parsing.

use rshell_interp::{Builtin, CallCtx};

pub struct Echo;

impl Builtin for Echo {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut newline = true;
        let mut interpret_escapes = false;
        let mut idx = 1usize; // skip argv[0]
        while idx < ctx.args.len() {
            let a = ctx.args[idx].as_slice();
            if a.len() < 2 || a[0] != b'-' {
                break;
            }
            // Must consist solely of -, n, e, E to count as a flag bundle.
            if a[1..].iter().all(|c| matches!(c, b'n' | b'e' | b'E')) {
                for c in &a[1..] {
                    match c {
                        b'n' => newline = false,
                        b'e' => interpret_escapes = true,
                        b'E' => interpret_escapes = false,
                        _ => unreachable!(),
                    }
                }
                idx += 1;
            } else {
                break;
            }
        }
        let mut first = true;
        for a in &ctx.args[idx..] {
            if !first {
                if let Err(e) = ctx.stdout.write_all(b" ") {
                    let _ = writeln!(ctx.stderr, "echo: {e}");
                    return 1;
                }
            }
            first = false;
            let payload = if interpret_escapes {
                interpret_backslash_escapes(a.as_slice())
            } else {
                a.to_vec()
            };
            if let Err(e) = ctx.stdout.write_all(&payload) {
                let _ = writeln!(ctx.stderr, "echo: {e}");
                return 1;
            }
        }
        if newline && let Err(e) = ctx.stdout.write_all(b"\n") {
            let _ = writeln!(ctx.stderr, "echo: {e}");
            return 1;
        }
        0
    }
}

fn interpret_backslash_escapes(input: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(input.len());
    let mut i = 0;
    while i < input.len() {
        if input[i] != b'\\' {
            out.push(input[i]);
            i += 1;
            continue;
        }
        if i + 1 >= input.len() {
            out.push(b'\\');
            i += 1;
            continue;
        }
        match input[i + 1] {
            b'a' => {
                out.push(7);
                i += 2;
            }
            b'b' => {
                out.push(8);
                i += 2;
            }
            b'c' => return out, // suppress further output
            b'e' | b'E' => {
                out.push(0x1b);
                i += 2;
            }
            b'f' => {
                out.push(12);
                i += 2;
            }
            b'n' => {
                out.push(b'\n');
                i += 2;
            }
            b'r' => {
                out.push(b'\r');
                i += 2;
            }
            b't' => {
                out.push(b'\t');
                i += 2;
            }
            b'v' => {
                out.push(11);
                i += 2;
            }
            b'\\' => {
                out.push(b'\\');
                i += 2;
            }
            b'0' => {
                // Octal: up to 3 octal digits after \0.
                i += 2;
                let mut val = 0u32;
                let mut n = 0;
                while n < 3 && i < input.len() && (b'0'..=b'7').contains(&input[i]) {
                    val = val * 8 + (input[i] - b'0') as u32;
                    i += 1;
                    n += 1;
                }
                out.push((val & 0xff) as u8);
            }
            b'x' => {
                // Hex: up to 2 hex digits.
                i += 2;
                let mut val = 0u32;
                let mut n = 0;
                while n < 2 && i < input.len() && input[i].is_ascii_hexdigit() {
                    val = val * 16 + (input[i] as char).to_digit(16).unwrap();
                    i += 1;
                    n += 1;
                }
                if n == 0 {
                    out.push(b'\\');
                    out.push(b'x');
                } else {
                    out.push((val & 0xff) as u8);
                }
            }
            other => {
                out.push(b'\\');
                out.push(other);
                i += 2;
            }
        }
    }
    out
}
