//! `wc` — count lines, words, and bytes in input files (or stdin).

use std::io::{Read, Write};

use rshell_interp::{Builtin, CallCtx};

pub struct Wc;

impl Builtin for Wc {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut lines = false;
        let mut words = false;
        let mut bytes = false;
        let mut chars = false;
        let mut max_line = false;
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
                let _ = ctx.stdout.write_all(b"Usage: wc [-l] [-w] [-c] [-m] [-L] [FILE]...\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 && !a[1..].iter().any(|c| !matches!(c, b'l' | b'w' | b'c' | b'm' | b'L')) {
                for f in &a[1..] {
                    match f {
                        b'l' => lines = true,
                        b'w' => words = true,
                        b'c' => bytes = true,
                        b'm' => chars = true,
                        b'L' => max_line = true,
                        _ => {}
                    }
                }
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        if !lines && !words && !bytes && !chars && !max_line {
            // Default: lines, words, bytes.
            lines = true;
            words = true;
            bytes = true;
        }

        let mut total_l = 0u64;
        let mut total_w = 0u64;
        let mut total_b = 0u64;
        let mut total_m = 0u64;
        let mut total_long = 0u64;
        let mut rc = 0;

        let show = Show { lines, words, bytes, chars, max_line };
        if files.is_empty() {
            let counts = match count_stream(ctx.stdin) {
                Ok(c) => c,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "wc: {e}");
                    return 1;
                }
            };
            print_line(ctx.stdout, &counts, None, &show);
            return 0;
        }
        let multiple = files.len() > 1;
        for f in files {
            let path = match std::str::from_utf8(f) {
                Ok(p) => p,
                Err(_) => {
                    let _ = ctx.stderr.write_all(b"wc: invalid path\n");
                    rc = 1;
                    continue;
                }
            };
            let mut reader: Box<dyn Read> = if path == "-" {
                Box::new(&mut *ctx.stdin)
            } else {
                match std::fs::File::open(path) {
                    Ok(f) => Box::new(f),
                    Err(e) => {
                        let _ = writeln!(ctx.stderr, "wc: {path}: {e}");
                        rc = 1;
                        continue;
                    }
                }
            };
            let counts = match count_stream(&mut *reader) {
                Ok(c) => c,
                Err(e) => {
                    let _ = writeln!(ctx.stderr, "wc: {path}: {e}");
                    rc = 1;
                    continue;
                }
            };
            total_l += counts.lines;
            total_w += counts.words;
            total_b += counts.bytes;
            total_m += counts.chars;
            total_long = total_long.max(counts.max_line);
            print_line(ctx.stdout, &counts, Some(path), &show);
        }
        if multiple {
            let total = Counts { lines: total_l, words: total_w, bytes: total_b, chars: total_m, max_line: total_long };
            print_line(ctx.stdout, &total, Some("total"), &show);
        }
        rc
    }
}

#[derive(Default)]
struct Counts {
    lines: u64,
    words: u64,
    bytes: u64,
    chars: u64,
    max_line: u64,
}

fn count_stream(src: &mut dyn Read) -> std::io::Result<Counts> {
    let mut buf = [0u8; 32 * 1024];
    let mut counts = Counts::default();
    let mut in_word = false;
    let mut current_line_len: u64 = 0;
    loop {
        let n = src.read(&mut buf)?;
        if n == 0 {
            break;
        }
        counts.bytes += n as u64;
        for &b in &buf[..n] {
            // chars: count UTF-8 starts (bytes that aren't continuation).
            if b & 0xc0 != 0x80 {
                counts.chars += 1;
            }
            if b == b'\n' {
                counts.lines += 1;
                counts.max_line = counts.max_line.max(current_line_len);
                current_line_len = 0;
                in_word = false;
            } else {
                current_line_len += 1;
                let is_ws = matches!(b, b' ' | b'\t' | 0x0b | 0x0c | b'\r');
                if is_ws {
                    in_word = false;
                } else if !in_word {
                    counts.words += 1;
                    in_word = true;
                }
            }
        }
    }
    counts.max_line = counts.max_line.max(current_line_len);
    Ok(counts)
}

struct Show {
    lines: bool,
    words: bool,
    bytes: bool,
    chars: bool,
    max_line: bool,
}

fn print_line(dst: &mut dyn Write, c: &Counts, name: Option<&str>, show: &Show) {
    let mut parts = Vec::<String>::new();
    if show.lines {
        parts.push(format!("{:>7}", c.lines));
    }
    if show.words {
        parts.push(format!("{:>7}", c.words));
    }
    if show.bytes {
        parts.push(format!("{:>7}", c.bytes));
    }
    if show.chars {
        parts.push(format!("{:>7}", c.chars));
    }
    if show.max_line {
        parts.push(format!("{:>7}", c.max_line));
    }
    let mut line = parts.join(" ");
    if let Some(n) = name {
        line.push(' ');
        line.push_str(n);
    }
    line.push('\n');
    let _ = dst.write_all(line.as_bytes());
}
