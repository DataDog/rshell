//! `printf` — format and print arguments. Supports the most-used specs:
//! `%s`, `%d`/`%i`, `%c`, `%x`/`%X`, `%o`, `%b`, `%%`, plus widths,
//! precision (`%.5s`), and bash-style argument cycling (the format
//! string is reused if there are extra arguments).

use rshell_interp::{Builtin, CallCtx};

pub struct Printf;

impl Builtin for Printf {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        if ctx.args.len() < 2 {
            let _ = ctx.stderr.write_all(b"printf: usage: printf format [arguments]\n");
            return 2;
        }
        let format = ctx.args[1].to_vec();
        let extras = &ctx.args[2..];
        let mut idx = 0usize;
        loop {
            match format_once(&format, extras, &mut idx, ctx) {
                Ok(consumed_any) => {
                    if !consumed_any || idx >= extras.len() {
                        return 0;
                    }
                }
                Err(rc) => return rc,
            }
        }
    }
}

fn format_once(
    format: &[u8],
    args: &[bstr::BString],
    idx: &mut usize,
    ctx: &mut CallCtx<'_>,
) -> Result<bool, i32> {
    let mut consumed_any_arg = false;
    let mut i = 0;
    while i < format.len() {
        let c = format[i];
        if c == b'\\' && i + 1 < format.len() {
            i += 1;
            let escaped = match format[i] {
                b'n' => b'\n',
                b't' => b'\t',
                b'r' => b'\r',
                b'\\' => b'\\',
                b'a' => 7,
                b'b' => 8,
                b'f' => 12,
                b'v' => 11,
                b'0' => 0,
                other => {
                    let _ = ctx.stdout.write_all(&[b'\\', other]);
                    i += 1;
                    continue;
                }
            };
            let _ = ctx.stdout.write_all(&[escaped]);
            i += 1;
            continue;
        }
        if c != b'%' {
            let _ = ctx.stdout.write_all(&[c]);
            i += 1;
            continue;
        }
        i += 1;
        if i >= format.len() {
            let _ = ctx.stdout.write_all(b"%");
            return Ok(consumed_any_arg);
        }
        let mut left_align = false;
        let mut zero_pad = false;
        while i < format.len() && matches!(format[i], b'-' | b'+' | b' ' | b'#' | b'0') {
            match format[i] {
                b'-' => left_align = true,
                b'0' => zero_pad = true,
                _ => {}
            }
            i += 1;
        }
        let mut width: Option<usize> = None;
        while i < format.len() && format[i].is_ascii_digit() {
            let v = width.unwrap_or(0) * 10 + (format[i] - b'0') as usize;
            width = Some(v);
            i += 1;
        }
        let mut precision: Option<usize> = None;
        if i < format.len() && format[i] == b'.' {
            i += 1;
            let mut p = 0usize;
            while i < format.len() && format[i].is_ascii_digit() {
                p = p * 10 + (format[i] - b'0') as usize;
                i += 1;
            }
            precision = Some(p);
        }
        if i >= format.len() {
            return Err(1);
        }
        let spec = format[i];
        i += 1;
        match spec {
            b'%' => {
                let _ = ctx.stdout.write_all(b"%");
            }
            b's' => {
                let arg = args.get(*idx).map(|a| a.as_slice()).unwrap_or(b"");
                if !arg.is_empty() {
                    consumed_any_arg = true;
                }
                *idx += 1;
                let bytes = match precision {
                    Some(p) => &arg[..p.min(arg.len())],
                    None => arg,
                };
                write_with_width(ctx, bytes, width, left_align, false);
            }
            b'd' | b'i' => {
                let arg = args.get(*idx).map(|a| a.as_slice()).unwrap_or(b"0");
                consumed_any_arg = true;
                *idx += 1;
                let s = std::str::from_utf8(arg).unwrap_or("0");
                let n: i64 = s.trim().parse().unwrap_or(0);
                let body = n.to_string();
                write_with_width(ctx, body.as_bytes(), width, left_align, zero_pad);
            }
            b'x' | b'X' | b'o' => {
                let arg = args.get(*idx).map(|a| a.as_slice()).unwrap_or(b"0");
                consumed_any_arg = true;
                *idx += 1;
                let s = std::str::from_utf8(arg).unwrap_or("0");
                let n: i64 = s.trim().parse().unwrap_or(0);
                let body = match spec {
                    b'x' => format!("{:x}", n as u64),
                    b'X' => format!("{:X}", n as u64),
                    b'o' => format!("{:o}", n as u64),
                    _ => unreachable!(),
                };
                write_with_width(ctx, body.as_bytes(), width, left_align, zero_pad);
            }
            b'c' => {
                let arg = args.get(*idx).map(|a| a.as_slice()).unwrap_or(b"");
                if !arg.is_empty() {
                    consumed_any_arg = true;
                    let _ = ctx.stdout.write_all(&arg[..1]);
                }
                *idx += 1;
            }
            b'b' => {
                let arg = args.get(*idx).map(|a| a.as_slice()).unwrap_or(b"");
                consumed_any_arg = true;
                *idx += 1;
                let interp = interpret_b_escapes(arg);
                let _ = ctx.stdout.write_all(&interp);
            }
            other => {
                let _ = ctx.stdout.write_all(&[b'%', other]);
            }
        }
    }
    Ok(consumed_any_arg)
}

fn write_with_width(
    ctx: &mut CallCtx<'_>,
    body: &[u8],
    width: Option<usize>,
    left_align: bool,
    zero_pad: bool,
) {
    if let Some(w) = width {
        if body.len() >= w {
            let _ = ctx.stdout.write_all(body);
            return;
        }
        let pad = w - body.len();
        let pad_byte = if zero_pad { b'0' } else { b' ' };
        if left_align {
            let _ = ctx.stdout.write_all(body);
            let _ = ctx.stdout.write_all(&vec![pad_byte; pad]);
        } else {
            let _ = ctx.stdout.write_all(&vec![pad_byte; pad]);
            let _ = ctx.stdout.write_all(body);
        }
    } else {
        let _ = ctx.stdout.write_all(body);
    }
}

fn interpret_b_escapes(input: &[u8]) -> Vec<u8> {
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
        let escaped = match input[i + 1] {
            b'n' => b'\n',
            b't' => b'\t',
            b'r' => b'\r',
            b'\\' => b'\\',
            b'a' => 7,
            b'b' => 8,
            b'f' => 12,
            b'v' => 11,
            b'0' => 0,
            _ => {
                out.push(b'\\');
                out.push(input[i + 1]);
                i += 2;
                continue;
            }
        };
        out.push(escaped);
        i += 2;
    }
    out
}
