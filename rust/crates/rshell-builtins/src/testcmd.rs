//! `test` / `[` — evaluate a conditional expression. Phase 4 baseline
//! covers the most-common operators: file tests (`-e`, `-f`, `-d`, `-r`,
//! `-w`, `-x`, `-s`), string tests (`-z`, `-n`, `=`, `!=`, `<`, `>`),
//! integer tests (`-eq`, `-ne`, `-lt`, `-le`, `-gt`, `-ge`), unary `!`,
//! and binary `-a` / `-o`.

use rshell_interp::{Builtin, CallCtx};

pub struct Test;

impl Builtin for Test {
    fn run(&self, ctx: &mut CallCtx<'_>) -> i32 {
        let mut argv: Vec<&[u8]> = ctx.args.iter().map(|a| a.as_slice()).collect();
        // For `[`, drop the leading `[` argv[0] and require the closing `]`.
        if argv[0] == b"[" {
            if argv.last() != Some(&b"]".as_slice()) {
let _ = ctx.stderr.write_all(b"[: missing ']'\n");
                return 2;
            }
            argv = argv[1..argv.len() - 1].to_vec();
        } else {
            argv = argv[1..].to_vec();
        }
        match eval_expr(&argv) {
            Ok(true) => 0,
            Ok(false) => 1,
            Err(e) => {
let _ = writeln!(ctx.stderr, "test: {e}");
                2
            }
        }
    }
}

fn eval_expr(args: &[&[u8]]) -> Result<bool, String> {
    // Full bash test grammar is intricate; this is a simplification that
    // covers the common cases. We split on `-a` / `-o` at the top level.
    if args.is_empty() {
        return Ok(false);
    }
    // Top-level OR.
    if let Some(idx) = top_split(args, b"-o") {
        let l = eval_expr(&args[..idx])?;
        let r = eval_expr(&args[idx + 1..])?;
        return Ok(l || r);
    }
    if let Some(idx) = top_split(args, b"-a") {
        let l = eval_expr(&args[..idx])?;
        let r = eval_expr(&args[idx + 1..])?;
        return Ok(l && r);
    }
    if args[0] == b"!" {
        return Ok(!eval_expr(&args[1..])?);
    }
    match args.len() {
        0 => Ok(false),
        1 => Ok(!args[0].is_empty()),
        2 => unary(args[0], args[1]),
        3 => binary(args[0], args[1], args[2]),
        _ => Err(format!("unsupported test expression of length {}", args.len())),
    }
}

fn top_split(args: &[&[u8]], op: &[u8]) -> Option<usize> {
    args.iter().position(|a| *a == op)
}

fn unary(op: &[u8], arg: &[u8]) -> Result<bool, String> {
    let path = std::str::from_utf8(arg).unwrap_or("");
    let metadata = || std::fs::metadata(path);
    Ok(match op {
        b"-z" => arg.is_empty(),
        b"-n" => !arg.is_empty(),
        b"-e" => metadata().is_ok(),
        b"-f" => metadata().map(|m| m.is_file()).unwrap_or(false),
        b"-d" => metadata().map(|m| m.is_dir()).unwrap_or(false),
        b"-s" => metadata().map(|m| m.len() > 0).unwrap_or(false),
        b"-r" | b"-w" | b"-x" => metadata().is_ok(), // simplified — we don't check perms
        _ => return Err(format!("unknown unary operator: {}", String::from_utf8_lossy(op))),
    })
}

fn binary(left: &[u8], op: &[u8], right: &[u8]) -> Result<bool, String> {
    Ok(match op {
        b"=" | b"==" => left == right,
        b"!=" => left != right,
        b"<" => left < right,
        b">" => left > right,
        b"-eq" => parse_int(left)? == parse_int(right)?,
        b"-ne" => parse_int(left)? != parse_int(right)?,
        b"-lt" => parse_int(left)? < parse_int(right)?,
        b"-le" => parse_int(left)? <= parse_int(right)?,
        b"-gt" => parse_int(left)? > parse_int(right)?,
        b"-ge" => parse_int(left)? >= parse_int(right)?,
        _ => return Err(format!("unknown binary operator: {}", String::from_utf8_lossy(op))),
    })
}

fn parse_int(s: &[u8]) -> Result<i64, String> {
    std::str::from_utf8(s)
        .map_err(|_| "non-utf8 integer".to_string())?
        .trim()
        .parse::<i64>()
        .map_err(|_| format!("not a number: {}", String::from_utf8_lossy(s)))
}
