//! `read` — read a line from stdin into one or more variables.
//!
//! Phase 4 baseline supports `-r` (raw, don't interpret backslashes).
//! Returns 0 on success, 1 on EOF.

use std::io::Read;

use bstr::BString;
use rshell_interp::{Builtin, CallCtx};

pub struct Read_;

impl Builtin for Read_ {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut raw = false;
        let mut names: Vec<BString> = Vec::new();
        let mut i = 1;
        while i < ctx.args.len() {
            let a = ctx.args[i].as_slice();
            if a == b"-r" {
                raw = true;
                i += 1;
                continue;
            }
            if a.starts_with(b"-") && a != b"--" {
                // Ignore unsupported flags rather than fail.
                i += 1;
                continue;
            }
            names.push(BString::from(a));
            i += 1;
        }
        if names.is_empty() {
            names.push(BString::from(b"REPLY".as_slice()));
        }

        let line = match read_line(ctx.stdin) {
            Ok(Some(l)) => l,
            Ok(None) => return 1, // EOF
            Err(_) => return 1,
        };
        let processed = if raw { line } else { unescape(&line) };

        // Split the line on IFS into len(names) fields; the last name
        // captures the rest.
        let ifs = ctx
            .env
            .get(b"IFS")
            .map_or(b" \t\n".to_vec(), |v| v.to_vec());
        let fields = split_fields(&processed, &ifs, names.len());
        for (idx, name) in names.iter().enumerate() {
            let value = fields
                .get(idx)
                .cloned()
                .unwrap_or_else(BString::default);
            ctx.env.set(name.clone(), value, false, false);
        }
        0
    }
}

fn read_line(src: &mut dyn Read) -> std::io::Result<Option<Vec<u8>>> {
    let mut buf = Vec::new();
    let mut byte = [0u8; 1];
    loop {
        let n = src.read(&mut byte)?;
        if n == 0 {
            if buf.is_empty() {
                return Ok(None);
            }
            return Ok(Some(buf));
        }
        if byte[0] == b'\n' {
            return Ok(Some(buf));
        }
        buf.push(byte[0]);
    }
}

fn unescape(line: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(line.len());
    let mut i = 0;
    while i < line.len() {
        if line[i] == b'\\' && i + 1 < line.len() {
            out.push(line[i + 1]);
            i += 2;
            continue;
        }
        out.push(line[i]);
        i += 1;
    }
    out
}

fn split_fields(line: &[u8], ifs: &[u8], n: usize) -> Vec<BString> {
    if n == 0 || ifs.is_empty() {
        return vec![BString::from(line)];
    }
    let mut fields: Vec<Vec<u8>> = Vec::new();
    let mut current = Vec::new();
    let mut i = 0;
    while i < line.len() && fields.len() < n - 1 {
        if ifs.contains(&line[i]) {
            // Skip leading whitespace IFS at field boundaries.
            if !current.is_empty() {
                fields.push(std::mem::take(&mut current));
            }
            // Consume run of IFS bytes.
            while i < line.len() && ifs.contains(&line[i]) {
                i += 1;
            }
            continue;
        }
        current.push(line[i]);
        i += 1;
    }
    if !current.is_empty() {
        fields.push(current);
    }
    // Last field gets the remainder, with leading IFS trimmed.
    let mut rest = line[i..].to_vec();
    while !rest.is_empty() && ifs.contains(&rest[0]) {
        rest.remove(0);
    }
    if !rest.is_empty() || fields.len() < n {
        fields.push(rest);
    }
    fields.into_iter().map(BString::from).collect()
}
