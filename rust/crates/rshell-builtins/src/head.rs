//! `head` — output the first N lines (default 10) or first N bytes of files.

use std::io::{Read, Write};

use rshell_interp::{Builtin, CallCtx};

pub struct Head;

impl Builtin for Head {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut lines: Option<usize> = None;
        let mut bytes: Option<usize> = None;
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
            if let Some(rest) = a.strip_prefix(b"--lines=") {
                lines = Some(parse_uint(rest));
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"--bytes=") {
                bytes = Some(parse_uint(rest));
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
            if a == b"--help" {
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: head [-n NUM] [-c NUM] [-q] [-v] [FILE]...\n");
                return 0;
            }
            if let Some(rest) = a.strip_prefix(b"-n") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"head: option requires an argument -- 'n'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                lines = Some(parse_uint(value));
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-c") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"head: option requires an argument -- 'c'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                bytes = Some(parse_uint(value));
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
            if a.starts_with(b"-") && a.len() > 1 && a[1..].iter().all(|c| c.is_ascii_digit()) {
                // Legacy form `-N` == `-n N`.
                lines = Some(parse_uint(&a[1..]));
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        let n_lines = lines.unwrap_or(10);
        let show_headers = (verbose || files.len() > 1) && !quiet;

        if files.is_empty() {
            let _ = head_stream(ctx.stdin, ctx.stdout, n_lines, bytes);
            return 0;
        }
        let mut rc = 0;
        let total = files.len();
        for (idx, fpath) in files.iter().enumerate() {
            let path = match std::str::from_utf8(fpath) {
                Ok(p) => p,
                Err(_) => {
                    let _ = ctx.stderr.write_all(b"head: invalid path\n");
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
            let mut f: Box<dyn Read> = if path == "-" {
                Box::new(&mut *ctx.stdin)
            } else {
                match std::fs::File::open(path) {
                    Ok(f) => Box::new(f),
                    Err(e) => {
                        let _ = writeln!(ctx.stderr, "head: {path}: {e}");
                        rc = 1;
                        continue;
                    }
                }
            };
            let _ = head_stream(&mut *f, ctx.stdout, n_lines, bytes);
            let _ = idx;
            let _ = total;
        }
        rc
    }
}

fn parse_uint(s: &[u8]) -> usize {
    let s = std::str::from_utf8(s).unwrap_or("0");
    s.trim().parse().unwrap_or(0)
}

fn head_stream(
    src: &mut dyn Read,
    dst: &mut dyn Write,
    lines: usize,
    bytes: Option<usize>,
) -> std::io::Result<()> {
    if let Some(n) = bytes {
        // Read up to n bytes and write.
        let mut remaining = n;
        let mut buf = [0u8; 8192];
        while remaining > 0 {
            let take = remaining.min(buf.len());
            let read = src.read(&mut buf[..take])?;
            if read == 0 {
                break;
            }
            dst.write_all(&buf[..read])?;
            remaining -= read;
        }
        return Ok(());
    }
    let mut buf = Vec::with_capacity(8192);
    let mut byte = [0u8; 1];
    let mut count = 0usize;
    while count < lines {
        let read = src.read(&mut byte)?;
        if read == 0 {
            break;
        }
        buf.push(byte[0]);
        if byte[0] == b'\n' {
            count += 1;
        }
        if buf.len() > 64 * 1024 {
            dst.write_all(&buf)?;
            buf.clear();
        }
    }
    if !buf.is_empty() {
        dst.write_all(&buf)?;
    }
    Ok(())
}
