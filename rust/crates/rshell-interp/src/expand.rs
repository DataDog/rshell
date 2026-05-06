//! Word expansion. Phase 3 baseline: supports literal text, single-quoted,
//! double-quoted, simple `$var` / `${var}`, special parameters (`$?`,
//! `$@`, `$*`, `$#`, `$$`, `$0`..`$9`), and ANSI-C / locale quoting.
//!
//! Not implemented in this minimum slice: parameter-expansion modifiers
//! (`${x:-y}`, `${x#p}`, etc.), arithmetic expansion, command
//! substitution, brace expansion, glob expansion. Those plug in as Phase
//! 3 grows.

use bstr::BString;
use rshell_parser::{Word, WordPart};

use crate::env::Env;

/// Expand a word into a single string (no field splitting). Used for
/// redirection targets, assignment values, case patterns, etc.
pub fn expand_to_string(env: &Env, word: &Word) -> BString {
    let mut out = Vec::new();
    expand_word_into(env, word, &mut out, false);
    BString::from(out)
}

/// Expand a word and split on IFS at top-level (i.e. unquoted) boundaries.
/// Returns one or more byte strings, suitable as arguments / iteration
/// items.
pub fn expand_to_fields(env: &Env, word: &Word) -> Vec<BString> {
    // Collect the "expanded characters", tagging each unit with whether
    // it came from a quoted context. Quoted bytes survive splitting.
    let mut tagged: Vec<(u8, bool)> = Vec::new(); // (byte, quoted)
    push_word_tagged(env, word, &mut tagged, false);
    let ifs = env.get(b"IFS").map_or(b" \t\n".to_vec(), |v| v.to_vec());
    if ifs.is_empty() {
        // No splitting.
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
            // Close the current field if any; collapse runs of whitespace
            // IFS bytes; non-whitespace IFS bytes always create a field
            // boundary even if empty.
            if current.is_some() {
                fields.push(current.take().unwrap());
            }
            // Skip whitespace runs; non-whitespace IFS produces an empty
            // field only when followed by another IFS or end.
            let mut saw_nonws = !is_ws(b);
            i += 1;
            while i < tagged.len() {
                let (b2, q2) = tagged[i];
                if q2 || !is_ifs(b2) {
                    break;
                }
                if !is_ws(b2) {
                    if saw_nonws {
                        // A second non-whitespace IFS byte: insert empty
                        // field between them.
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

fn expand_word_into(env: &Env, word: &Word, out: &mut Vec<u8>, _quoted_ctx: bool) {
    for p in &word.parts {
        expand_part_into(env, p, out, false);
    }
}

fn expand_part_into(env: &Env, part: &WordPart, out: &mut Vec<u8>, quoted_ctx: bool) {
    match part {
        WordPart::Literal(s) => out.extend_from_slice(s),
        WordPart::SingleQuoted(s) => out.extend_from_slice(s),
        WordPart::DoubleQuoted(parts) => {
            for inner in parts {
                expand_part_into(env, inner, out, true);
            }
        }
        WordPart::AnsiCQuoted(s) => out.extend_from_slice(s),
        WordPart::LocaleQuoted(s) => out.extend_from_slice(s),
        WordPart::DollarVar(name) => {
            push_dollar_var(env, name.as_slice(), out, quoted_ctx);
        }
        WordPart::DollarBrace(body) => {
            // Phase 3 baseline: only support a bare name inside `${...}`.
            // Modifiers (`:-`, `:=`, `#`, etc.) land in a follow-up.
            push_dollar_var(env, body.as_slice(), out, quoted_ctx);
        }
        WordPart::DollarParen(_)
        | WordPart::Backtick(_)
        | WordPart::DollarDoubleParen(_)
        | WordPart::ProcSubst { .. }
        | WordPart::ExtGlob { .. } => {
            // Unsupported in the Phase 3 baseline. Inserting nothing is
            // the safest no-op; the runtime is responsible for raising a
            // diagnostic when such a part is encountered in a context
            // that requires it.
        }
    }
}

fn push_word_tagged(env: &Env, word: &Word, out: &mut Vec<(u8, bool)>, _quoted_ctx: bool) {
    for p in &word.parts {
        push_part_tagged(env, p, out, false);
    }
}

fn push_part_tagged(env: &Env, part: &WordPart, out: &mut Vec<(u8, bool)>, quoted: bool) {
    match part {
        WordPart::Literal(s) => out.extend(s.iter().map(|&b| (b, quoted))),
        WordPart::SingleQuoted(s) => out.extend(s.iter().map(|&b| (b, true))),
        WordPart::DoubleQuoted(parts) => {
            for inner in parts {
                push_part_tagged(env, inner, out, true);
            }
        }
        WordPart::AnsiCQuoted(s) | WordPart::LocaleQuoted(s) => {
            out.extend(s.iter().map(|&b| (b, true)));
        }
        WordPart::DollarVar(name) => {
            let mut buf = Vec::new();
            push_dollar_var(env, name.as_slice(), &mut buf, quoted);
            out.extend(buf.into_iter().map(|b| (b, quoted)));
        }
        WordPart::DollarBrace(body) => {
            let mut buf = Vec::new();
            push_dollar_var(env, body.as_slice(), &mut buf, quoted);
            out.extend(buf.into_iter().map(|b| (b, quoted)));
        }
        WordPart::DollarParen(_)
        | WordPart::Backtick(_)
        | WordPart::DollarDoubleParen(_)
        | WordPart::ProcSubst { .. }
        | WordPart::ExtGlob { .. } => {}
    }
}

fn push_dollar_var(env: &Env, name: &[u8], out: &mut Vec<u8>, _quoted: bool) {
    match name {
        b"?" => out.extend_from_slice(env.last_exit.to_string().as_bytes()),
        b"$" => out.extend_from_slice(env.pid.to_string().as_bytes()),
        b"#" => out.extend_from_slice(env.argc().to_string().as_bytes()),
        b"@" | b"*" => {
            // For the baseline we collapse positional params with a single
            // space; full IFS behaviour comes with the splitter rewrite.
            for (i, a) in env.args.iter().enumerate() {
                if i > 0 {
                    out.push(b' ');
                }
                out.extend_from_slice(a);
            }
        }
        b"0" => {
            // `$0` is the script/program name. We use a placeholder for
            // now; the CLI sets it explicitly when known.
            if let Some(v) = env.get(b"0") {
                out.extend_from_slice(v);
            }
        }
        n if n.len() == 1 && n[0].is_ascii_digit() => {
            let idx = (n[0] - b'0') as usize;
            if idx > 0 && idx <= env.args.len() {
                out.extend_from_slice(&env.args[idx - 1]);
            }
        }
        _ => {
            if let Some(v) = env.get(name) {
                out.extend_from_slice(v);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rshell_parser::parse_script;

    fn parse_word(src: &str) -> Word {
        // Parse as a simple command and pull the first word.
        let s = parse_script(src.as_bytes()).expect("parse");
        let stmt = s.stmts.into_iter().next().unwrap();
        match stmt.command {
            rshell_parser::Command::Simple(c) => c.words.into_iter().next().unwrap(),
            _ => panic!("expected simple command"),
        }
    }

    #[test]
    fn literal() {
        let env = Env::new();
        assert_eq!(
            expand_to_string(&env, &parse_word("hello")).as_slice(),
            b"hello"
        );
    }

    #[test]
    fn dollar_var() {
        let mut env = Env::new();
        env.set("FOO".into(), "bar".into(), false, false);
        assert_eq!(
            expand_to_string(&env, &parse_word("$FOO")).as_slice(),
            b"bar"
        );
        assert_eq!(
            expand_to_string(&env, &parse_word("${FOO}")).as_slice(),
            b"bar"
        );
    }

    #[test]
    fn double_quoted_var() {
        let mut env = Env::new();
        env.set("FOO".into(), "x y".into(), false, false);
        // Single field even though value contains a space.
        let fields = expand_to_fields(&env, &parse_word("\"$FOO\""));
        assert_eq!(fields.len(), 1);
        assert_eq!(fields[0].as_slice(), b"x y");
    }

    #[test]
    fn unquoted_var_splits() {
        let mut env = Env::new();
        env.set("FOO".into(), "x y z".into(), false, false);
        let fields = expand_to_fields(&env, &parse_word("$FOO"));
        assert_eq!(fields.len(), 3);
        assert_eq!(fields[0].as_slice(), b"x");
    }
}
