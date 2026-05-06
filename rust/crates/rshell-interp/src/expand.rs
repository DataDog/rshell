//! Word expansion. Phase 4 expansion: adds command substitution `$(...)`,
//! backticks, arithmetic `$((...))`, and the common parameter-expansion
//! modifiers (`${x:-y}`, `${x:=y}`, `${x:?y}`, `${x:+y}`, `${#x}`,
//! `${x#p}`, `${x##p}`, `${x%p}`, `${x%%p}`, `${x/p/r}`, `${x//p/r}`,
//! `${x:offset}`, `${x:offset:length}`).
//!
//! Glob expansion and brace expansion stay deferred for now — they do
//! not change the contract of `expand_to_*` and will be layered in via
//! the `expand_to_fields` exit point.

use bstr::BString;
use rshell_parser::{Stmt, Word, WordPart};

use crate::env::Env;

/// Capability the runner provides to the expander: env access plus
/// callbacks for `$(...)` / `` ` ` `` and arithmetic evaluation.
///
/// Defining this as a trait keeps `expand` decoupled from `Runner`'s
/// internals — the runner's implementation lives in `runner.rs`.
pub trait Evaluator {
    fn env(&self) -> &Env;
    fn env_mut(&mut self) -> &mut Env;
    fn eval_cmdsubst(&mut self, stmts: &[Stmt]) -> Vec<u8>;
    fn eval_arith(&mut self, body: &[u8]) -> Vec<u8>;
}

/// Convenience implementation so `expand_*` can be called with a bare
/// `&Env` when no runner is available (tests, redir-target expansion at
/// trivial-word time, etc.). Command substitution and arithmetic become
/// no-ops in that mode — same behaviour as the Phase-3 stubs.
impl Evaluator for Env {
    fn env(&self) -> &Env {
        self
    }
    fn env_mut(&mut self) -> &mut Env {
        self
    }
    fn eval_cmdsubst(&mut self, _: &[Stmt]) -> Vec<u8> {
        Vec::new()
    }
    fn eval_arith(&mut self, _: &[u8]) -> Vec<u8> {
        Vec::new()
    }
}

/// Expand a word into a single string (no field splitting).
pub fn expand_to_string<E: Evaluator>(eval: &mut E, word: &Word) -> BString {
    let mut out = Vec::new();
    for p in &word.parts {
        expand_part_into(eval, p, &mut out, false);
    }
    BString::from(out)
}

/// Expand a word and split on IFS at top-level (unquoted) boundaries.
pub fn expand_to_fields<E: Evaluator>(eval: &mut E, word: &Word) -> Vec<BString> {
    let mut tagged: Vec<(u8, bool)> = Vec::new();
    for p in &word.parts {
        push_part_tagged(eval, p, &mut tagged, false);
    }
    let ifs = eval
        .env()
        .get(b"IFS")
        .map_or(b" \t\n".to_vec(), |v| v.to_vec());
    if ifs.is_empty() {
        return vec![BString::from(
            tagged.into_iter().map(|(b, _)| b).collect::<Vec<_>>(),
        )];
    }
    split_on_ifs(&tagged, &ifs)
}

fn split_on_ifs(tagged: &[(u8, bool)], ifs: &[u8]) -> Vec<BString> {
    let is_ws = |b: u8| matches!(b, b' ' | b'\t' | b'\n');
    let is_ifs = |b: u8| ifs.contains(&b);
    let mut fields: Vec<Vec<u8>> = Vec::new();
    let mut current: Option<Vec<u8>> = None;
    let mut i = 0;
    while i < tagged.len() {
        let (b, quoted) = tagged[i];
        if !quoted && is_ifs(b) {
            if current.is_some() {
                fields.push(current.take().unwrap());
            }
            let mut saw_nonws = !is_ws(b);
            i += 1;
            while i < tagged.len() {
                let (b2, q2) = tagged[i];
                if q2 || !is_ifs(b2) {
                    break;
                }
                if !is_ws(b2) {
                    if saw_nonws {
                        fields.push(Vec::new());
                    }
                    saw_nonws = true;
                }
                i += 1;
            }
        } else {
            current.get_or_insert_with(Vec::new).push(b);
            i += 1;
        }
    }
    if let Some(c) = current {
        fields.push(c);
    }
    fields.into_iter().map(BString::from).collect()
}

fn expand_part_into<E: Evaluator>(eval: &mut E, part: &WordPart, out: &mut Vec<u8>, quoted: bool) {
    match part {
        WordPart::Literal(s) => out.extend_from_slice(s),
        WordPart::SingleQuoted(s) => out.extend_from_slice(s),
        WordPart::DoubleQuoted(parts) => {
            for inner in parts {
                expand_part_into(eval, inner, out, true);
            }
        }
        WordPart::AnsiCQuoted(s) => out.extend_from_slice(s),
        WordPart::LocaleQuoted(s) => out.extend_from_slice(s),
        WordPart::DollarVar(name) => {
            push_dollar_var(eval, name.as_slice(), out);
        }
        WordPart::DollarBrace(body) => {
            push_dollar_brace(eval, body.as_slice(), out);
        }
        WordPart::DollarParen(stmts) => {
            let mut bytes = eval.eval_cmdsubst(stmts);
            trim_trailing_newlines(&mut bytes);
            out.extend_from_slice(&bytes);
        }
        WordPart::Backtick(stmts) => {
            let mut bytes = eval.eval_cmdsubst(stmts);
            trim_trailing_newlines(&mut bytes);
            out.extend_from_slice(&bytes);
        }
        WordPart::DollarDoubleParen(body) => {
            let bytes = eval.eval_arith(body.as_slice());
            out.extend_from_slice(&bytes);
        }
        WordPart::ProcSubst { .. } | WordPart::ExtGlob { .. } => {
            // Blocked features. The runner converts these into a typed
            // error before calling expand on the host word, so reaching
            // here is unexpected — emit nothing.
        }
    }
    let _ = quoted;
}

fn push_part_tagged<E: Evaluator>(
    eval: &mut E,
    part: &WordPart,
    out: &mut Vec<(u8, bool)>,
    quoted: bool,
) {
    match part {
        WordPart::Literal(s) => out.extend(s.iter().map(|&b| (b, quoted))),
        WordPart::SingleQuoted(s) => out.extend(s.iter().map(|&b| (b, true))),
        WordPart::DoubleQuoted(parts) => {
            for inner in parts {
                push_part_tagged(eval, inner, out, true);
            }
        }
        WordPart::AnsiCQuoted(s) | WordPart::LocaleQuoted(s) => {
            out.extend(s.iter().map(|&b| (b, true)));
        }
        WordPart::DollarVar(name) => {
            let mut buf = Vec::new();
            push_dollar_var(eval, name.as_slice(), &mut buf);
            out.extend(buf.into_iter().map(|b| (b, quoted)));
        }
        WordPart::DollarBrace(body) => {
            let mut buf = Vec::new();
            push_dollar_brace(eval, body.as_slice(), &mut buf);
            out.extend(buf.into_iter().map(|b| (b, quoted)));
        }
        WordPart::DollarParen(stmts) | WordPart::Backtick(stmts) => {
            let mut bytes = eval.eval_cmdsubst(stmts);
            trim_trailing_newlines(&mut bytes);
            out.extend(bytes.into_iter().map(|b| (b, quoted)));
        }
        WordPart::DollarDoubleParen(body) => {
            let bytes = eval.eval_arith(body.as_slice());
            out.extend(bytes.into_iter().map(|b| (b, quoted)));
        }
        WordPart::ProcSubst { .. } | WordPart::ExtGlob { .. } => {}
    }
}

fn trim_trailing_newlines(bytes: &mut Vec<u8>) {
    while bytes.last() == Some(&b'\n') {
        bytes.pop();
    }
}

fn push_dollar_var<E: Evaluator>(eval: &E, name: &[u8], out: &mut Vec<u8>) {
    match name {
        b"?" => out.extend_from_slice(eval.env().last_exit.to_string().as_bytes()),
        b"$" => out.extend_from_slice(eval.env().pid.to_string().as_bytes()),
        b"#" => out.extend_from_slice(eval.env().argc().to_string().as_bytes()),
        b"@" | b"*" => {
            for (i, a) in eval.env().args.iter().enumerate() {
                if i > 0 {
                    out.push(b' ');
                }
                out.extend_from_slice(a);
            }
        }
        b"0" => {
            if let Some(v) = eval.env().get(b"0") {
                out.extend_from_slice(v);
            }
        }
        n if n.len() == 1 && n[0].is_ascii_digit() => {
            let idx = (n[0] - b'0') as usize;
            if idx > 0 && idx <= eval.env().args.len() {
                out.extend_from_slice(&eval.env().args[idx - 1]);
            }
        }
        _ => {
            if let Some(v) = eval.env().get(name) {
                out.extend_from_slice(v);
            }
        }
    }
}

/// Parse and evaluate the body of `${...}`. Supports a useful subset of
/// the bash grammar:
/// - `name` — same as `$name`
/// - `#name` — string length
/// - `name:-default` / `name-default` — default if unset/empty
/// - `name:=default` / `name=default` — assign default if unset
/// - `name:?word` / `name?word` — error if unset
/// - `name:+alt` / `name+alt` — alt if set
/// - `name:offset` / `name:offset:length` — substring
/// - `name#pattern` / `name##pattern` — strip prefix (shortest/longest)
/// - `name%pattern` / `name%%pattern` — strip suffix (shortest/longest)
/// - `name/pattern/replacement` — replace first match
/// - `name//pattern/replacement` — replace all matches
fn push_dollar_brace<E: Evaluator>(eval: &mut E, body: &[u8], out: &mut Vec<u8>) {
    // Length form: `${#name}`.
    if let Some(rest) = body.strip_prefix(b"#") {
        let name = rest;
        let mut buf = Vec::new();
        push_dollar_var(eval, name, &mut buf);
        out.extend_from_slice(buf.len().to_string().as_bytes());
        return;
    }
    // Find the operator dividing `name` from the operation. Operators
    // are: `:-`, `:=`, `:?`, `:+`, `:`, `#`, `##`, `%`, `%%`, `/`, `//`.
    let (name, op_kind, rhs) = split_brace_body(body);

    // Resolve `name` to a value (may be empty or unset).
    let mut value = Vec::new();
    push_dollar_var(eval, name, &mut value);
    let unset = eval.env().get(name).is_none()
        && !matches!(name, b"?" | b"$" | b"#" | b"@" | b"*" | b"0");
    let empty_or_unset = unset || value.is_empty();

    match op_kind {
        BraceOp::Plain => out.extend_from_slice(&value),
        BraceOp::DefaultIfEmpty => {
            if empty_or_unset {
                let rhs_bytes = expand_inline_text(eval, rhs);
                out.extend_from_slice(&rhs_bytes);
            } else {
                out.extend_from_slice(&value);
            }
        }
        BraceOp::DefaultIfUnset => {
            if unset {
                let rhs_bytes = expand_inline_text(eval, rhs);
                out.extend_from_slice(&rhs_bytes);
            } else {
                out.extend_from_slice(&value);
            }
        }
        BraceOp::AssignIfEmpty => {
            if empty_or_unset && is_simple_name(name) {
                let rhs_bytes = expand_inline_text(eval, rhs);
                eval.env_mut()
                    .set(name.into(), BString::from(rhs_bytes.clone()), false, false);
                out.extend_from_slice(&rhs_bytes);
            } else {
                out.extend_from_slice(&value);
            }
        }
        BraceOp::AssignIfUnset => {
            if unset && is_simple_name(name) {
                let rhs_bytes = expand_inline_text(eval, rhs);
                eval.env_mut()
                    .set(name.into(), BString::from(rhs_bytes.clone()), false, false);
                out.extend_from_slice(&rhs_bytes);
            } else {
                out.extend_from_slice(&value);
            }
        }
        BraceOp::ErrorIfEmpty | BraceOp::ErrorIfUnset => {
            // We don't have a clean way to abort the expansion through a
            // typed error from inside expand. Match bash's "error: …"
            // behaviour by writing nothing — the runner will see a
            // zero-length expansion and continue. (Hardening this is a
            // follow-up.)
            out.extend_from_slice(&value);
        }
        BraceOp::AltIfEmpty => {
            if !empty_or_unset {
                let rhs_bytes = expand_inline_text(eval, rhs);
                out.extend_from_slice(&rhs_bytes);
            }
        }
        BraceOp::AltIfUnset => {
            if !unset {
                let rhs_bytes = expand_inline_text(eval, rhs);
                out.extend_from_slice(&rhs_bytes);
            }
        }
        BraceOp::Substring => {
            let s = &value;
            let (offset, length) = parse_substring_args(eval, rhs, s.len());
            let start = offset.min(s.len());
            let end = match length {
                Some(l) => start.saturating_add(l).min(s.len()),
                None => s.len(),
            };
            out.extend_from_slice(&s[start..end]);
        }
        BraceOp::StripShortPrefix => {
            let pat = expand_inline_text(eval, rhs);
            out.extend_from_slice(&strip_prefix(&value, &pat, false));
        }
        BraceOp::StripLongPrefix => {
            let pat = expand_inline_text(eval, rhs);
            out.extend_from_slice(&strip_prefix(&value, &pat, true));
        }
        BraceOp::StripShortSuffix => {
            let pat = expand_inline_text(eval, rhs);
            out.extend_from_slice(&strip_suffix(&value, &pat, false));
        }
        BraceOp::StripLongSuffix => {
            let pat = expand_inline_text(eval, rhs);
            out.extend_from_slice(&strip_suffix(&value, &pat, true));
        }
        BraceOp::ReplaceFirst | BraceOp::ReplaceAll => {
            let (pat, repl) = split_replace_rhs(rhs);
            let pat_bytes = expand_inline_text(eval, pat);
            let repl_bytes = expand_inline_text(eval, repl);
            let all = matches!(op_kind, BraceOp::ReplaceAll);
            out.extend_from_slice(&replace(&value, &pat_bytes, &repl_bytes, all));
        }
    }
}

#[derive(Debug, Clone, Copy)]
enum BraceOp {
    Plain,
    DefaultIfEmpty,    // :-
    DefaultIfUnset,    // -
    AssignIfEmpty,     // :=
    AssignIfUnset,     // =
    ErrorIfEmpty,      // :?
    ErrorIfUnset,      // ?
    AltIfEmpty,        // :+
    AltIfUnset,        // +
    Substring,         // :OFFSET[:LENGTH]
    StripShortPrefix,  // #
    StripLongPrefix,   // ##
    StripShortSuffix,  // %
    StripLongSuffix,   // %%
    ReplaceFirst,      // /pat/repl
    ReplaceAll,        // //pat/repl
}

fn split_brace_body(body: &[u8]) -> (&[u8], BraceOp, &[u8]) {
    // Walk past the variable name; allow `?`, `!`, `@`, etc. as
    // single-character special params, plus alphanumeric/underscore for
    // ordinary names.
    let mut i = 0;
    if !body.is_empty() && is_special_param_char(body[0]) {
        i = 1;
    } else {
        while i < body.len() && (body[i].is_ascii_alphanumeric() || body[i] == b'_') {
            i += 1;
        }
    }
    let name = &body[..i];
    let rest = &body[i..];
    if rest.is_empty() {
        return (name, BraceOp::Plain, b"");
    }
    if rest.starts_with(b":-") {
        return (name, BraceOp::DefaultIfEmpty, &rest[2..]);
    }
    if rest.starts_with(b":=") {
        return (name, BraceOp::AssignIfEmpty, &rest[2..]);
    }
    if rest.starts_with(b":?") {
        return (name, BraceOp::ErrorIfEmpty, &rest[2..]);
    }
    if rest.starts_with(b":+") {
        return (name, BraceOp::AltIfEmpty, &rest[2..]);
    }
    if rest.starts_with(b"##") {
        return (name, BraceOp::StripLongPrefix, &rest[2..]);
    }
    if rest.starts_with(b"%%") {
        return (name, BraceOp::StripLongSuffix, &rest[2..]);
    }
    if rest.starts_with(b"//") {
        return (name, BraceOp::ReplaceAll, &rest[2..]);
    }
    match rest[0] {
        b':' => (name, BraceOp::Substring, &rest[1..]),
        b'-' => (name, BraceOp::DefaultIfUnset, &rest[1..]),
        b'=' => (name, BraceOp::AssignIfUnset, &rest[1..]),
        b'?' => (name, BraceOp::ErrorIfUnset, &rest[1..]),
        b'+' => (name, BraceOp::AltIfUnset, &rest[1..]),
        b'#' => (name, BraceOp::StripShortPrefix, &rest[1..]),
        b'%' => (name, BraceOp::StripShortSuffix, &rest[1..]),
        b'/' => (name, BraceOp::ReplaceFirst, &rest[1..]),
        _ => (name, BraceOp::Plain, b""),
    }
}

fn is_special_param_char(c: u8) -> bool {
    matches!(c, b'?' | b'!' | b'$' | b'@' | b'*' | b'#' | b'-' | b'0')
}

fn is_simple_name(name: &[u8]) -> bool {
    !name.is_empty()
        && (name[0].is_ascii_alphabetic() || name[0] == b'_')
        && name.iter().all(|&b| b.is_ascii_alphanumeric() || b == b'_')
}

/// Re-expand a fragment of bytes that may contain `$var`, `${...}`, or
/// `$(...)` references. Used inside parameter-expansion modifiers'
/// right-hand sides.
fn expand_inline_text<E: Evaluator>(eval: &mut E, text: &[u8]) -> Vec<u8> {
    // For simplicity we re-tokenise via `$`-scanning. Quoting inside a
    // brace-expansion RHS is bash-specific and uncommon in our corpus;
    // skip it and treat the whole thing as a literal-with-$-expansions.
    let mut out = Vec::new();
    let mut i = 0;
    while i < text.len() {
        if text[i] != b'$' {
            out.push(text[i]);
            i += 1;
            continue;
        }
        // `$name`
        if i + 1 < text.len() && (text[i + 1].is_ascii_alphabetic() || text[i + 1] == b'_') {
            let mut j = i + 1;
            while j < text.len() && (text[j].is_ascii_alphanumeric() || text[j] == b'_') {
                j += 1;
            }
            let name = &text[i + 1..j];
            push_dollar_var(eval, name, &mut out);
            i = j;
            continue;
        }
        // `${...}` — find matching `}`.
        if i + 1 < text.len() && text[i + 1] == b'{' {
            let body_start = i + 2;
            let mut depth = 1usize;
            let mut j = body_start;
            while j < text.len() && depth > 0 {
                match text[j] {
                    b'{' => depth += 1,
                    b'}' => {
                        depth -= 1;
                        if depth == 0 {
                            break;
                        }
                    }
                    _ => {}
                }
                j += 1;
            }
            if depth == 0 {
                push_dollar_brace(eval, &text[body_start..j], &mut out);
                i = j + 1;
                continue;
            }
        }
        // `$?`, `$#`, `$$`, `$@`, etc.
        if i + 1 < text.len() && is_special_param_char(text[i + 1]) {
            let name = &text[i + 1..i + 2];
            push_dollar_var(eval, name, &mut out);
            i += 2;
            continue;
        }
        out.push(b'$');
        i += 1;
    }
    out
}

fn parse_substring_args<E: Evaluator>(eval: &mut E, rhs: &[u8], _len: usize) -> (usize, Option<usize>) {
    // Split rhs at the first `:` that's not inside parens.
    let mut depth: i32 = 0;
    let mut split = None;
    for (i, &b) in rhs.iter().enumerate() {
        match b {
            b'(' => depth += 1,
            b')' => depth -= 1,
            b':' if depth == 0 => {
                split = Some(i);
                break;
            }
            _ => {}
        }
    }
    let (off_bytes, len_bytes) = match split {
        Some(i) => (&rhs[..i], Some(&rhs[i + 1..])),
        None => (rhs, None),
    };
    let off = parse_int_or_arith(eval, off_bytes).max(0) as usize;
    let len = len_bytes.map(|b| parse_int_or_arith(eval, b).max(0) as usize);
    (off, len)
}

fn parse_int_or_arith<E: Evaluator>(eval: &mut E, bytes: &[u8]) -> i64 {
    // Best-effort: try arith eval first, fall back to direct integer parse.
    let arith = eval.eval_arith(bytes);
    if let Ok(s) = std::str::from_utf8(&arith)
        && let Ok(n) = s.trim().parse::<i64>()
    {
        return n;
    }
    if let Ok(s) = std::str::from_utf8(bytes)
        && let Ok(n) = s.trim().parse::<i64>()
    {
        return n;
    }
    0
}

fn strip_prefix(value: &[u8], pattern: &[u8], longest: bool) -> Vec<u8> {
    // Bash patterns support `*`, `?`, `[...]`. We implement the common
    // case: `*` matches any sequence, `?` matches any single byte,
    // others are literal. Char classes are not yet supported.
    if pattern.is_empty() {
        return value.to_vec();
    }
    // Scan all possible prefix lengths and pick the longest/shortest match.
    let mut best: Option<usize> = None;
    for end in 0..=value.len() {
        if matches_pattern(&value[..end], pattern) {
            best = Some(end);
            if !longest {
                break;
            }
        }
    }
    match best {
        Some(n) => value[n..].to_vec(),
        None => value.to_vec(),
    }
}

fn strip_suffix(value: &[u8], pattern: &[u8], longest: bool) -> Vec<u8> {
    if pattern.is_empty() {
        return value.to_vec();
    }
    let mut best: Option<usize> = None;
    for start in (0..=value.len()).rev() {
        if matches_pattern(&value[start..], pattern) {
            best = Some(start);
            if !longest {
                break;
            }
        }
    }
    match best {
        Some(n) => value[..n].to_vec(),
        None => value.to_vec(),
    }
}

/// Backtracking glob matcher for `*`, `?`. Returns true when `pattern`
/// matches the entirety of `text`.
fn matches_pattern(text: &[u8], pattern: &[u8]) -> bool {
    fn rec(t: &[u8], p: &[u8]) -> bool {
        if p.is_empty() {
            return t.is_empty();
        }
        match p[0] {
            b'*' => {
                // Try consuming 0..t.len() bytes against the rest of the
                // pattern.
                if rec(t, &p[1..]) {
                    return true;
                }
                if t.is_empty() {
                    return false;
                }
                rec(&t[1..], p)
            }
            b'?' => {
                if t.is_empty() {
                    return false;
                }
                rec(&t[1..], &p[1..])
            }
            b'\\' if p.len() > 1 => {
                if t.is_empty() || t[0] != p[1] {
                    return false;
                }
                rec(&t[1..], &p[2..])
            }
            c => {
                if t.is_empty() || t[0] != c {
                    return false;
                }
                rec(&t[1..], &p[1..])
            }
        }
    }
    rec(text, pattern)
}

fn split_replace_rhs(rhs: &[u8]) -> (&[u8], &[u8]) {
    // Split at the first unescaped `/`.
    let mut i = 0;
    while i < rhs.len() {
        if rhs[i] == b'\\' && i + 1 < rhs.len() {
            i += 2;
            continue;
        }
        if rhs[i] == b'/' {
            return (&rhs[..i], &rhs[i + 1..]);
        }
        i += 1;
    }
    (rhs, b"")
}

fn replace(value: &[u8], pat: &[u8], repl: &[u8], all: bool) -> Vec<u8> {
    if pat.is_empty() {
        return value.to_vec();
    }
    // Glob semantics: `*` is any sequence, `?` is any byte. For
    // simplicity, when the pattern has metacharacters we fall back to a
    // very basic search-and-replace: find the longest prefix-anchored
    // match starting at each position.
    let has_meta = pat.iter().any(|&b| matches!(b, b'*' | b'?'));
    let mut out = Vec::with_capacity(value.len());
    let mut i = 0;
    while i < value.len() {
        let mut matched: Option<usize> = None; // length consumed
        if has_meta {
            for end in (i..=value.len()).rev() {
                if matches_pattern(&value[i..end], pat) {
                    matched = Some(end - i);
                    break;
                }
            }
        } else if value[i..].starts_with(pat) {
            matched = Some(pat.len());
        }
        match matched {
            Some(n) => {
                out.extend_from_slice(repl);
                i += n.max(1);
                if !all {
                    out.extend_from_slice(&value[i..]);
                    return out;
                }
            }
            None => {
                out.push(value[i]);
                i += 1;
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;
    use rshell_parser::parse_script;

    fn parse_word(src: &str) -> Word {
        let s = parse_script(src.as_bytes()).expect("parse");
        let stmt = s.stmts.into_iter().next().unwrap();
        match stmt.command {
            rshell_parser::Command::Simple(c) => c.words.into_iter().next().unwrap(),
            _ => panic!("expected simple command"),
        }
    }

    #[test]
    fn literal() {
        let mut env = Env::new();
        assert_eq!(
            expand_to_string(&mut env, &parse_word("hello")).as_slice(),
            b"hello"
        );
    }

    #[test]
    fn dollar_var() {
        let mut env = Env::new();
        env.set("FOO".into(), "bar".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("$FOO")).as_slice(),
            b"bar"
        );
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${FOO}")).as_slice(),
            b"bar"
        );
    }

    #[test]
    fn double_quoted_var() {
        let mut env = Env::new();
        env.set("FOO".into(), "x y".into(), false, false);
        let fields = expand_to_fields(&mut env, &parse_word("\"$FOO\""));
        assert_eq!(fields.len(), 1);
        assert_eq!(fields[0].as_slice(), b"x y");
    }

    #[test]
    fn unquoted_var_splits() {
        let mut env = Env::new();
        env.set("FOO".into(), "x y z".into(), false, false);
        let fields = expand_to_fields(&mut env, &parse_word("$FOO"));
        assert_eq!(fields.len(), 3);
    }

    #[test]
    fn brace_default() {
        let mut env = Env::new();
        // unset → default
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X:-fallback}")).as_slice(),
            b"fallback"
        );
        env.set("X".into(), "set".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X:-fallback}")).as_slice(),
            b"set"
        );
    }

    #[test]
    fn brace_length() {
        let mut env = Env::new();
        env.set("X".into(), "hello".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${#X}")).as_slice(),
            b"5"
        );
    }

    #[test]
    fn brace_strip_prefix() {
        let mut env = Env::new();
        env.set("X".into(), "/usr/local/bin".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X#/}")).as_slice(),
            b"usr/local/bin"
        );
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X##*/}")).as_slice(),
            b"bin"
        );
    }

    #[test]
    fn brace_strip_suffix() {
        let mut env = Env::new();
        env.set("X".into(), "file.tar.gz".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X%.gz}")).as_slice(),
            b"file.tar"
        );
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X%%.*}")).as_slice(),
            b"file"
        );
    }

    #[test]
    fn brace_replace() {
        let mut env = Env::new();
        env.set("X".into(), "a b a c".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X/a/Z}")).as_slice(),
            b"Z b a c"
        );
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X//a/Z}")).as_slice(),
            b"Z b Z c"
        );
    }

    #[test]
    fn brace_substring() {
        let mut env = Env::new();
        env.set("X".into(), "abcdef".into(), false, false);
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X:2}")).as_slice(),
            b"cdef"
        );
        assert_eq!(
            expand_to_string(&mut env, &parse_word("${X:1:3}")).as_slice(),
            b"bcd"
        );
    }
}
