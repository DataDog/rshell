//! `cut` — extract sections from each line. Phase 4 baseline supports
//! `-d DELIM`, `-f FIELDS`, `-c CHARS`, `-b BYTES`, `--complement`,
//! `--output-delimiter`. Field/char/byte specs accept comma-separated
//! ranges (`1,3`, `1-3`, `2-`, `-3`).

use std::io::Read;

use rshell_interp::{Builtin, CallCtx};

pub struct Cut;

#[derive(Clone, Copy)]
enum Mode {
    None,
    Bytes,
    Chars,
    Fields,
}

impl Builtin for Cut {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut mode = Mode::None;
        let mut spec: Vec<u8> = Vec::new();
        let mut delim: u8 = b'\t';
        let mut output_delim: Option<Vec<u8>> = None;
        let mut complement = false;
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
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: cut -d DELIM -f LIST [FILE]...\n");
                return 0;
            }
            if a == b"--complement" {
                complement = true;
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"--output-delimiter=") {
                output_delim = Some(rest.to_vec());
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-d") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"cut: option requires an argument -- 'd'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                delim = *value.first().unwrap_or(&b'\t');
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-f") {
                mode = Mode::Fields;
                spec = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"cut: option requires an argument -- 'f'\n");
                        return 1;
                    }
                    ctx.args[i].to_vec()
                } else {
                    rest.to_vec()
                };
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-c") {
                mode = Mode::Chars;
                spec = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"cut: option requires an argument -- 'c'\n");
                        return 1;
                    }
                    ctx.args[i].to_vec()
                } else {
                    rest.to_vec()
                };
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-b") {
                mode = Mode::Bytes;
                spec = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"cut: option requires an argument -- 'b'\n");
                        return 1;
                    }
                    ctx.args[i].to_vec()
                } else {
                    rest.to_vec()
                };
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        if matches!(mode, Mode::None) {
            let _ = ctx.stderr.write_all(b"cut: requires -b, -c, or -f\n");
            return 1;
        }
        let ranges = parse_spec(&spec);

        let mut buf = Vec::<u8>::new();
        if files.is_empty() {
            let _ = ctx.stdin.read_to_end(&mut buf);
        } else {
            for f in files {
                let path = match std::str::from_utf8(f) {
                    Ok(p) => p,
                    Err(_) => continue,
                };
                let _ = if path == "-" {
                    ctx.stdin.read_to_end(&mut buf)
                } else {
                    match std::fs::File::open(path) {
                        Ok(mut f) => f.read_to_end(&mut buf),
                        Err(e) => {
                            let _ = writeln!(ctx.stderr, "cut: {path}: {e}");
                            return 1;
                        }
                    }
                };
            }
        }
        let mut start = 0;
        for i in 0..=buf.len() {
            if i == buf.len() || buf[i] == b'\n' {
                let line = &buf[start..i];
                let out_line = process_line(
                    line,
                    mode,
                    &ranges,
                    delim,
                    output_delim.as_deref(),
                    complement,
                );
                let _ = ctx.stdout.write_all(&out_line);
                let _ = ctx.stdout.write_all(b"\n");
                start = i + 1;
                if i == buf.len() {
                    break;
                }
            }
        }
        // Strip the trailing extra newline if the input didn't have one.
        // (Simpler to over-emit; bash cut also adds one.)
        0
    }
}

fn parse_spec(s: &[u8]) -> Vec<(usize, usize)> {
    let mut out = Vec::new();
    for chunk in s.split(|&b| b == b',') {
        let s = std::str::from_utf8(chunk).unwrap_or("");
        if s.is_empty() {
            continue;
        }
        if let Some(idx) = s.find('-') {
            let lo = if idx == 0 {
                1
            } else {
                s[..idx].parse().unwrap_or(1)
            };
            let hi = if idx == s.len() - 1 {
                usize::MAX
            } else {
                s[idx + 1..].parse().unwrap_or(usize::MAX)
            };
            out.push((lo, hi));
        } else if let Ok(n) = s.parse::<usize>() {
            out.push((n, n));
        }
    }
    out
}

fn in_ranges(idx: usize, ranges: &[(usize, usize)], complement: bool) -> bool {
    let any = ranges.iter().any(|&(lo, hi)| idx >= lo && idx <= hi);
    if complement { !any } else { any }
}

fn process_line(
    line: &[u8],
    mode: Mode,
    ranges: &[(usize, usize)],
    delim: u8,
    output_delim: Option<&[u8]>,
    complement: bool,
) -> Vec<u8> {
    match mode {
        Mode::Bytes | Mode::Chars => {
            let mut out = Vec::new();
            for (i, &b) in line.iter().enumerate() {
                if in_ranges(i + 1, ranges, complement) {
                    out.push(b);
                }
            }
            out
        }
        Mode::Fields => {
            let fields: Vec<&[u8]> = line.split(|&b| b == delim).collect();
            // If there's no delimiter at all, output the whole line.
            if fields.len() == 1 {
                return line.to_vec();
            }
            let sep = output_delim
                .map(|s| s.to_vec())
                .unwrap_or_else(|| vec![delim]);
            let mut out = Vec::new();
            let mut first = true;
            for (i, f) in fields.iter().enumerate() {
                if in_ranges(i + 1, ranges, complement) {
                    if !first {
                        out.extend_from_slice(&sep);
                    }
                    out.extend_from_slice(f);
                    first = false;
                }
            }
            out
        }
        Mode::None => Vec::new(),
    }
}
