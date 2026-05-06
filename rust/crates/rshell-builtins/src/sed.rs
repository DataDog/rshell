//! `sed` — minimal substitute-only implementation.
//!
//! Phase 4 baseline supports the `s/PATTERN/REPL/[gI]` command via `-e`
//! or as the first non-flag argument. Multi-script chaining (`-e ... -e
//! ...`), regex flags `g` and `i`, and reading from stdin / files are
//! supported. `-n` (quiet) is honoured. Other commands (`d`, `p`, `y/`,
//! address ranges) are out of scope for this iteration.

use rshell_interp::{Builtin, CallCtx};

pub struct Sed;

impl Builtin for Sed {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut quiet = false;
        let mut scripts: Vec<&[u8]> = Vec::new();
        let mut files: Vec<&[u8]> = Vec::new();
        let mut i = 1;
        let mut have_script = false;
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
                    .write_all(b"Usage: sed [-n] [-e SCRIPT] SCRIPT [FILE]...\n");
                return 0;
            }
            if a == b"-n" {
                quiet = true;
                i += 1;
                continue;
            }
            if let Some(rest) = a.strip_prefix(b"-e") {
                let value: &[u8] = if rest.is_empty() {
                    i += 1;
                    if i >= ctx.args.len() {
                        let _ = ctx
                            .stderr
                            .write_all(b"sed: option requires an argument -- 'e'\n");
                        return 1;
                    }
                    ctx.args[i].as_slice()
                } else {
                    rest
                };
                scripts.push(value);
                have_script = true;
                i += 1;
                continue;
            }
            if !have_script {
                scripts.push(a);
                have_script = true;
                i += 1;
                continue;
            }
            files.push(a);
            i += 1;
        }
        if scripts.is_empty() {
            let _ = ctx.stderr.write_all(b"sed: missing script\n");
            return 1;
        }

        let parsed: Vec<SubScript> = match scripts
            .iter()
            .map(|s| parse_subst(s))
            .collect::<Result<Vec<_>, _>>()
        {
            Ok(v) => v,
            Err(e) => {
                let _ = writeln!(ctx.stderr, "sed: {e}");
                return 1;
            }
        };

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
                } else {
                    match std::fs::read(path) {
                        Ok(b) => buf.extend_from_slice(&b),
                        Err(e) => {
                            let _ = writeln!(ctx.stderr, "sed: {path}: {e}");
                            return 1;
                        }
                    }
                }
            }
        }
        let mut start = 0;
        for i in 0..=buf.len() {
            if i == buf.len() || buf[i] == b'\n' {
                let mut line = buf[start..i].to_vec();
                let mut matched = false;
                for s in &parsed {
                    let (new_line, did) = apply_subst(s, &line);
                    line = new_line;
                    if did {
                        matched = true;
                    }
                }
                if !quiet || matched {
                    let _ = ctx.stdout.write_all(&line);
                    let _ = ctx.stdout.write_all(b"\n");
                }
                start = i + 1;
                if i == buf.len() {
                    break;
                }
            }
        }
        0
    }
}

struct SubScript {
    pattern: regex::bytes::Regex,
    repl: Vec<u8>,
    global: bool,
}

fn parse_subst(script: &[u8]) -> Result<SubScript, String> {
    if script.is_empty() {
        return Err("empty script".into());
    }
    if script[0] != b's' {
        return Err(format!(
            "only `s/pat/repl/flags` is supported (got: {})",
            String::from_utf8_lossy(script)
        ));
    }
    if script.len() < 4 {
        return Err("malformed s command".into());
    }
    let sep = script[1];
    // Find next two unescaped separators.
    let mut parts = Vec::new();
    let mut buf = Vec::new();
    let mut i = 2;
    while i < script.len() && parts.len() < 2 {
        if script[i] == b'\\' && i + 1 < script.len() {
            buf.push(script[i + 1]);
            i += 2;
            continue;
        }
        if script[i] == sep {
            parts.push(std::mem::take(&mut buf));
            i += 1;
            continue;
        }
        buf.push(script[i]);
        i += 1;
    }
    if parts.len() < 2 {
        return Err("malformed s command".into());
    }
    let flags = &script[i..];
    let mut global = false;
    let mut ignore_case = false;
    for &f in flags {
        match f {
            b'g' => global = true,
            b'I' | b'i' => ignore_case = true,
            _ => {}
        }
    }
    let pat = std::str::from_utf8(&parts[0])
        .map_err(|_| "pattern must be UTF-8".to_string())?
        .to_string();
    let mut builder = regex::bytes::RegexBuilder::new(&pat);
    builder.case_insensitive(ignore_case);
    let pattern = builder.build().map_err(|e| e.to_string())?;
    Ok(SubScript {
        pattern,
        repl: parts.into_iter().nth(1).unwrap_or_default(),
        global,
    })
}

fn apply_subst(s: &SubScript, input: &[u8]) -> (Vec<u8>, bool) {
    let mut out = Vec::new();
    let mut pos = 0;
    let mut matched = false;
    while pos <= input.len() {
        if let Some(m) = s.pattern.find(&input[pos..]) {
            out.extend_from_slice(&input[pos..pos + m.start()]);
            // Apply replacement, handling `\1`..`\9` and `&` references.
            apply_repl(&s.repl, &input[pos..], m, &mut out);
            pos += m.end();
            matched = true;
            if !s.global {
                out.extend_from_slice(&input[pos..]);
                return (out, true);
            }
            // Avoid infinite loop on zero-width matches.
            if m.start() == m.end() {
                if pos < input.len() {
                    out.push(input[pos]);
                    pos += 1;
                } else {
                    break;
                }
            }
        } else {
            out.extend_from_slice(&input[pos..]);
            break;
        }
    }
    (out, matched)
}

fn apply_repl(repl: &[u8], _line: &[u8], _m: regex::bytes::Match, out: &mut Vec<u8>) {
    // Phase 4 baseline: literal replacement only, with `\\`, `\&`, `&`
    // (whole match), `\n` (newline).
    let mut i = 0;
    while i < repl.len() {
        if repl[i] == b'\\' && i + 1 < repl.len() {
            match repl[i + 1] {
                b'\\' => out.push(b'\\'),
                b'&' => out.push(b'&'),
                b'n' => out.push(b'\n'),
                b't' => out.push(b'\t'),
                _ => {
                    out.push(b'\\');
                    out.push(repl[i + 1]);
                }
            }
            i += 2;
            continue;
        }
        if repl[i] == b'&' {
            out.extend_from_slice(_m.as_bytes());
            i += 1;
            continue;
        }
        out.push(repl[i]);
        i += 1;
    }
}
