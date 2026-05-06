//! `tr` — translate, squeeze, or delete characters.
//!
//! Phase 4 baseline supports `-d`, `-s`, `-c` (complement), and the
//! common character classes (`[:lower:]`, `[:upper:]`, `[:digit:]`,
//! `[:alpha:]`, `[:alnum:]`, `[:space:]`, `[:punct:]`) plus character
//! ranges (`a-z`).

use rshell_interp::{Builtin, CallCtx};

pub struct Tr;

impl Builtin for Tr {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut delete = false;
        let mut squeeze = false;
        let mut complement = false;
        let mut truncate = false;
        let mut positional: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"--" {
                i += 1;
                while i < ctx.args.len() {
                    positional.push(ctx.args[i].as_slice());
                    i += 1;
                }
                break;
            }
            if a == b"--help" {
                let _ = ctx
                    .stdout
                    .write_all(b"Usage: tr [-d] [-s] [-c] [-t] SET1 [SET2]\n");
                return 0;
            }
            if a.starts_with(b"-") && a.len() > 1 {
                let mut ok = true;
                for f in &a[1..] {
                    match f {
                        b'd' => delete = true,
                        b's' => squeeze = true,
                        b'c' | b'C' => complement = true,
                        b't' => truncate = true,
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
            positional.push(a);
            i += 1;
        }
        if positional.is_empty() {
            let _ = ctx.stderr.write_all(b"tr: missing operand\n");
            return 1;
        }
        let set1 = expand_set(positional[0]);
        let set2 = positional.get(1).map(|s| expand_set(s));

        let mut buf = Vec::new();
        if let Err(e) = ctx.stdin.read_to_end(&mut buf) {
            let _ = writeln!(ctx.stderr, "tr: {e}");
            return 1;
        }

        let in_set1 = |b: u8| -> bool {
            let m = set1.contains(&b);
            if complement { !m } else { m }
        };

        let mut out = Vec::with_capacity(buf.len());
        if delete {
            for b in &buf {
                if !in_set1(*b) {
                    out.push(*b);
                }
            }
            if squeeze && let Some(s2) = &set2 {
                out = squeeze_run(&out, s2);
            }
        } else if let Some(s2) = &set2 {
            // Translate set1[i] -> set2[i], with set2 padded by repeating
            // its last byte (or truncated when -t).
            for b in &buf {
                if in_set1(*b) {
                    let idx = if complement {
                        // For complement, all in-complement bytes map to s2[last].
                        s2.len().saturating_sub(1)
                    } else {
                        set1.iter().position(|x| x == b).unwrap_or(0)
                    };
                    let mapped = if idx < s2.len() {
                        s2[idx]
                    } else if truncate {
                        *b
                    } else {
                        *s2.last().unwrap_or(b)
                    };
                    out.push(mapped);
                } else {
                    out.push(*b);
                }
            }
            if squeeze {
                out = squeeze_run(&out, s2);
            }
        } else if squeeze {
            out = squeeze_run(&buf, &set1);
        } else {
            // Single set, no -d, no -s, no set2: error.
            let _ = ctx.stderr.write_all(b"tr: missing operand after SET1\n");
            return 1;
        }
        let _ = ctx.stdout.write_all(&out);
        0
    }
}

fn squeeze_run(buf: &[u8], set: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(buf.len());
    let mut prev: Option<u8> = None;
    for &b in buf {
        if set.contains(&b) && prev == Some(b) {
            continue;
        }
        out.push(b);
        prev = Some(b);
    }
    out
}

fn expand_set(s: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < s.len() {
        // Character class: `[:name:]`
        if s[i] == b'[' && s.len() > i + 1 && s[i + 1] == b':' {
            if let Some(close) = find_subseq(&s[i..], b":]") {
                let name = &s[i + 2..i + close];
                expand_class(name, &mut out);
                i += close + 2;
                continue;
            }
        }
        // Range: `a-z`
        if i + 2 < s.len() && s[i + 1] == b'-' {
            let lo = s[i];
            let hi = s[i + 2];
            for c in lo..=hi {
                out.push(c);
            }
            i += 3;
            continue;
        }
        // Backslash escapes.
        if s[i] == b'\\' && i + 1 < s.len() {
            let escaped = match s[i + 1] {
                b'n' => b'\n',
                b't' => b'\t',
                b'r' => b'\r',
                b'\\' => b'\\',
                b'0' => 0,
                other => other,
            };
            out.push(escaped);
            i += 2;
            continue;
        }
        out.push(s[i]);
        i += 1;
    }
    out
}

fn find_subseq(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack.windows(needle.len()).position(|w| w == needle)
}

fn expand_class(name: &[u8], out: &mut Vec<u8>) {
    match name {
        b"lower" => out.extend(b'a'..=b'z'),
        b"upper" => out.extend(b'A'..=b'Z'),
        b"digit" => out.extend(b'0'..=b'9'),
        b"alpha" => {
            out.extend(b'A'..=b'Z');
            out.extend(b'a'..=b'z');
        }
        b"alnum" => {
            out.extend(b'0'..=b'9');
            out.extend(b'A'..=b'Z');
            out.extend(b'a'..=b'z');
        }
        b"space" => out.extend_from_slice(b" \t\n\x0b\x0c\r"),
        b"blank" => out.extend_from_slice(b" \t"),
        b"punct" => {
            out.extend_from_slice(b"!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~");
        }
        b"cntrl" => {
            for c in 0..=31 {
                out.push(c);
            }
            out.push(127);
        }
        b"print" => {
            for c in 32..=126 {
                out.push(c);
            }
        }
        b"graph" => {
            for c in 33..=126 {
                out.push(c);
            }
        }
        b"xdigit" => {
            out.extend(b'0'..=b'9');
            out.extend(b'a'..=b'f');
            out.extend(b'A'..=b'F');
        }
        _ => {}
    }
}
