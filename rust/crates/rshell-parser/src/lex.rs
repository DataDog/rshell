//! Tokenizer for shell scripts.
//!
//! The lexer parses *words* (with full quoting and expansion structure) and
//! emits them alongside operator, newline, and here-doc tokens. The parser
//! is responsible for higher-level recognition (reserved words, statement
//! boundaries, etc.) and detecting numeric fd-prefix tokens that
//! immediately precede a redirection operator (we expose byte spans so the
//! parser can detect the adjacency).

use std::fmt;

use bstr::BString;

use crate::ast::{ExtGlobOp, HereDocBody, ProcSubstDir, Word, WordPart};

#[derive(Debug, thiserror::Error, PartialEq, Eq, Clone)]
pub struct LexError {
    pub pos: usize,
    pub message: String,
}

impl fmt::Display for LexError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "lex error at byte {}: {}", self.pos, self.message)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Span {
    pub start: usize,
    pub end: usize,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Op {
    Pipe,        // |
    PipeAmp,     // |&
    Amp,         // &
    AndAnd,      // &&
    OrOr,        // ||
    Semi,        // ;
    SemiSemi,    // ;;
    SemiAmp,     // ;&
    SemiSemiAmp, // ;;&
    LParen,      // (
    RParen,      // )
    Less,        // <
    Great,       // >
    DGreat,      // >>
    DLess,       // <<
    DLessDash,   // <<-
    TLess,       // <<<
    LessGreat,   // <>
    LessAmp,     // <&
    GreatAmp,    // >&
    AmpGreat,    // &>
    AmpDGreat,   // &>>
    GreatPipe,   // >|
}

#[derive(Debug, Clone)]
pub enum Token {
    Word {
        word: Word,
        span: Span,
    },
    /// Numeric fd prefix immediately followed by a redirection operator.
    FdPrefix {
        fd: u32,
        span: Span,
    },
    Op {
        op: Op,
        span: Span,
    },
    Newline {
        span: Span,
    },
    HereDocBody {
        body: HereDocBody,
        span: Span,
    },
    Eof {
        pos: usize,
    },
}

#[derive(Debug, Clone)]
struct PendingHeredoc {
    delim: BString,
    quoted_delim: bool,
    strip_tabs: bool,
}

pub struct Lexer<'a> {
    src: &'a [u8],
    pos: usize,
    pending_heredocs: Vec<PendingHeredoc>,
    /// `Some(strip)` after a `<<` / `<<-` operator is emitted, until the
    /// parser registers the delimiter via `register_pending_heredoc`.
    expecting_heredoc: Option<bool>,
    buffered: Vec<Token>,
    finished: bool,
}

impl<'a> Lexer<'a> {
    pub fn new(src: &'a [u8]) -> Self {
        Self {
            src,
            pos: 0,
            pending_heredocs: Vec::new(),
            expecting_heredoc: None,
            buffered: Vec::new(),
            finished: false,
        }
    }

    /// Called by the parser after it consumes the word that follows a
    /// `<<` / `<<-` operator. Records the here-doc body delimiter so the
    /// next newline triggers body consumption.
    pub fn register_pending_heredoc(&mut self, delim: BString, quoted: bool) {
        if let Some(strip) = self.expecting_heredoc.take() {
            self.pending_heredocs.push(PendingHeredoc {
                delim,
                quoted_delim: quoted,
                strip_tabs: strip,
            });
        }
    }

    pub fn next_token(&mut self) -> Result<Token, LexError> {
        if let Some(tok) = self.buffered.pop() {
            return Ok(tok);
        }
        if self.finished {
            return Ok(Token::Eof { pos: self.pos });
        }

        // Skip blanks, line continuations, and comments at the top level.
        loop {
            match self.peek() {
                Some(b' ' | b'\t') => self.pos += 1,
                Some(b'\\') if self.peek_at(1) == Some(b'\n') => self.pos += 2,
                Some(b'#') => {
                    while let Some(c) = self.peek() {
                        if c == b'\n' {
                            break;
                        }
                        self.pos += 1;
                    }
                }
                _ => break,
            }
        }

        if self.pos >= self.src.len() {
            self.finished = true;
            return Ok(Token::Eof { pos: self.pos });
        }

        let start = self.pos;
        let c = self.src[self.pos];

        if c == b'\n' {
            self.pos += 1;
            let span = Span {
                start,
                end: self.pos,
            };
            // Drain queued here-docs before yielding the newline so the
            // body tokens follow it on subsequent calls.
            if !self.pending_heredocs.is_empty() {
                let pendings: Vec<_> = self.pending_heredocs.drain(..).collect();
                let mut hd_tokens = Vec::with_capacity(pendings.len());
                for p in &pendings {
                    let body = self.read_heredoc_body(p)?;
                    hd_tokens.push(Token::HereDocBody {
                        body,
                        span: Span {
                            start: self.pos,
                            end: self.pos,
                        },
                    });
                }
                hd_tokens.reverse();
                self.buffered.extend(hd_tokens);
            }
            return Ok(Token::Newline { span });
        }

        if let Some((op, len)) = self.try_operator() {
            self.pos += len;
            let span = Span {
                start,
                end: self.pos,
            };
            // Note when this operator opens a here-doc; the parser will
            // call `register_pending_heredoc` after consuming the delim.
            match op {
                Op::DLess => self.expecting_heredoc = Some(false),
                Op::DLessDash => self.expecting_heredoc = Some(true),
                _ => {}
            }
            return Ok(Token::Op { op, span });
        }

        // Numeric fd prefix immediately followed by a redirection operator.
        if c.is_ascii_digit() {
            let (digits_end, num_str) = scan_digits(self.src, self.pos);
            if let Some((op, op_len)) = lookup_redir_after_digits(&self.src[digits_end..])
                && let Ok(fd) = num_str.parse::<u32>()
            {
                let fd_span = Span {
                    start,
                    end: digits_end,
                };
                let op_start = digits_end;
                self.pos = digits_end + op_len;
                let op_span = Span {
                    start: op_start,
                    end: self.pos,
                };
                match op {
                    Op::DLess => self.expecting_heredoc = Some(false),
                    Op::DLessDash => self.expecting_heredoc = Some(true),
                    _ => {}
                }
                self.buffered.push(Token::Op { op, span: op_span });
                return Ok(Token::FdPrefix { fd, span: fd_span });
            }
        }

        let (word, end) = self.read_word()?;
        Ok(Token::Word {
            word,
            span: Span { start, end },
        })
    }

    fn peek(&self) -> Option<u8> {
        self.src.get(self.pos).copied()
    }
    fn peek_at(&self, off: usize) -> Option<u8> {
        self.src.get(self.pos + off).copied()
    }

    fn try_operator(&mut self) -> Option<(Op, usize)> {
        let s = &self.src[self.pos..];
        // Process substitution `<(` / `>(` is a *word*, not a redirection
        // operator. Suppress the `<` / `>` operator match so the word
        // reader takes over.
        if s.starts_with(b"<(") || s.starts_with(b">(") {
            return None;
        }
        if s.starts_with(b";;&") {
            return Some((Op::SemiSemiAmp, 3));
        }
        if s.starts_with(b"<<<") {
            return Some((Op::TLess, 3));
        }
        if s.starts_with(b"<<-") {
            return Some((Op::DLessDash, 3));
        }
        if s.starts_with(b"&>>") {
            return Some((Op::AmpDGreat, 3));
        }
        if s.starts_with(b"&&") {
            return Some((Op::AndAnd, 2));
        }
        if s.starts_with(b"||") {
            return Some((Op::OrOr, 2));
        }
        if s.starts_with(b"|&") {
            return Some((Op::PipeAmp, 2));
        }
        if s.starts_with(b";;") {
            return Some((Op::SemiSemi, 2));
        }
        if s.starts_with(b";&") {
            return Some((Op::SemiAmp, 2));
        }
        if s.starts_with(b"<<") {
            return Some((Op::DLess, 2));
        }
        if s.starts_with(b">>") {
            return Some((Op::DGreat, 2));
        }
        if s.starts_with(b"<>") {
            return Some((Op::LessGreat, 2));
        }
        if s.starts_with(b"<&") {
            return Some((Op::LessAmp, 2));
        }
        if s.starts_with(b">&") {
            return Some((Op::GreatAmp, 2));
        }
        if s.starts_with(b"&>") {
            return Some((Op::AmpGreat, 2));
        }
        if s.starts_with(b">|") {
            return Some((Op::GreatPipe, 2));
        }
        match s.first().copied()? {
            b'|' => Some((Op::Pipe, 1)),
            b'&' => Some((Op::Amp, 1)),
            b';' => Some((Op::Semi, 1)),
            b'(' => Some((Op::LParen, 1)),
            b')' => Some((Op::RParen, 1)),
            b'<' => Some((Op::Less, 1)),
            b'>' => Some((Op::Great, 1)),
            _ => None,
        }
    }

    fn read_heredoc_body(&mut self, p: &PendingHeredoc) -> Result<HereDocBody, LexError> {
        let mut body = Vec::new();
        loop {
            let line_start = self.pos;
            let mut line_end = line_start;
            while line_end < self.src.len() && self.src[line_end] != b'\n' {
                line_end += 1;
            }
            let line: &[u8] = if p.strip_tabs {
                let mut s = &self.src[line_start..line_end];
                while let Some((b'\t', rest)) = s.split_first() {
                    s = rest;
                }
                s
            } else {
                &self.src[line_start..line_end]
            };
            if line == p.delim.as_slice() {
                self.pos = line_end;
                if self.pos < self.src.len() && self.src[self.pos] == b'\n' {
                    self.pos += 1;
                }
                break;
            }
            if line_end == self.src.len() {
                body.extend_from_slice(line);
                self.pos = line_end;
                break;
            }
            body.extend_from_slice(line);
            body.push(b'\n');
            self.pos = line_end + 1;
        }
        if p.quoted_delim {
            Ok(HereDocBody {
                quoted_delim: true,
                parts: vec![WordPart::Literal(BString::from(body))],
            })
        } else {
            let parts = parse_dquoted_body(&body, 0)?;
            Ok(HereDocBody {
                quoted_delim: false,
                parts,
            })
        }
    }

    fn read_word(&mut self) -> Result<(Word, usize), LexError> {
        let s = self.src;
        let mut parts: Vec<WordPart> = Vec::new();
        let mut lit = Vec::<u8>::new();
        let mut i = self.pos;
        while i < s.len() {
            let c = s[i];
            // Process substitution `<(...)` / `>(...)` only at a position
            // where it could syntactically be one. We accept it
            // unconditionally inside a word (matches bash).
            if (c == b'<' || c == b'>') && s.get(i + 1) == Some(&b'(') {
                if !lit.is_empty() {
                    parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                }
                let dir = if c == b'<' {
                    ProcSubstDir::In
                } else {
                    ProcSubstDir::Out
                };
                let body_start = i + 2;
                let body_end = find_matching_paren(s, body_start, 0)?;
                let body_bytes = &s[body_start..body_end];
                let stmts =
                    crate::parse::parse_inner_script(body_bytes, body_start).map_err(|e| {
                        LexError {
                            pos: e.pos,
                            message: format!("process substitution: {}", e.message),
                        }
                    })?;
                parts.push(WordPart::ProcSubst {
                    direction: dir,
                    body: stmts,
                });
                i = body_end + 1;
                continue;
            }
            // Extended-glob operators: `?(`, `*(`, `+(`, `@(`, `!(`.
            if matches!(c, b'?' | b'*' | b'+' | b'@' | b'!') && s.get(i + 1) == Some(&b'(') {
                let op = match c {
                    b'?' => ExtGlobOp::Once,
                    b'*' => ExtGlobOp::Star,
                    b'+' => ExtGlobOp::Plus,
                    b'@' => ExtGlobOp::At,
                    b'!' => ExtGlobOp::Not,
                    _ => unreachable!(),
                };
                if !lit.is_empty() {
                    parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                }
                let body_start = i + 2;
                let body_end = find_matching_paren(s, body_start, 0)?;
                parts.push(WordPart::ExtGlob {
                    op,
                    body: BString::from(&s[body_start..body_end]),
                });
                i = body_end + 1;
                continue;
            }
            if is_word_delim(c) {
                break;
            }
            match c {
                b'\\' => {
                    if i + 1 < s.len() {
                        let n = s[i + 1];
                        if n == b'\n' {
                            i += 2;
                            continue;
                        }
                        lit.push(n);
                        i += 2;
                        continue;
                    }
                    lit.push(b'\\');
                    i += 1;
                }
                b'\'' => {
                    if !lit.is_empty() {
                        parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                    }
                    let body_start = i + 1;
                    let mut j = body_start;
                    while j < s.len() && s[j] != b'\'' {
                        j += 1;
                    }
                    if j == s.len() {
                        return Err(LexError {
                            pos: i,
                            message: "unterminated single-quoted string".into(),
                        });
                    }
                    parts.push(WordPart::SingleQuoted(BString::from(&s[body_start..j])));
                    i = j + 1;
                }
                b'"' => {
                    if !lit.is_empty() {
                        parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                    }
                    let body_start = i + 1;
                    let mut j = body_start;
                    while j < s.len() {
                        match s[j] {
                            b'"' => break,
                            b'\\' if j + 1 < s.len() => j += 2,
                            b'$' if s.get(j + 1) == Some(&b'(') => {
                                let (_p, consumed) = if s.get(j + 2) == Some(&b'(') {
                                    parse_dollar_double_paren(s, j, 0)?
                                } else {
                                    parse_dollar_paren(s, j, 0)?
                                };
                                j += consumed;
                            }
                            b'$' if s.get(j + 1) == Some(&b'{') => {
                                let (_p, consumed) = parse_dollar_brace(s, j, 0)?;
                                j += consumed;
                            }
                            b'`' => {
                                let (_p, consumed) = parse_backtick(s, j, 0)?;
                                j += consumed;
                            }
                            _ => j += 1,
                        }
                    }
                    if j == s.len() {
                        return Err(LexError {
                            pos: i,
                            message: "unterminated double-quoted string".into(),
                        });
                    }
                    let inner = parse_dquoted_body(&s[body_start..j], body_start)?;
                    parts.push(WordPart::DoubleQuoted(inner));
                    i = j + 1;
                }
                b'$' => {
                    if !lit.is_empty() {
                        parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                    }
                    let (part, consumed) = parse_dollar(s, i, 0)?;
                    parts.push(part);
                    i += consumed;
                }
                b'`' => {
                    if !lit.is_empty() {
                        parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                    }
                    let (part, consumed) = parse_backtick(s, i, 0)?;
                    parts.push(part);
                    i += consumed;
                }
                _ => {
                    lit.push(c);
                    i += 1;
                }
            }
        }
        if !lit.is_empty() {
            parts.push(WordPart::Literal(BString::from(lit)));
        }
        let word = Word { parts };
        let end = i;
        self.pos = end;
        Ok((word, end))
    }
}

// --- private helpers ---

fn scan_digits(src: &[u8], start: usize) -> (usize, &str) {
    let mut end = start;
    while end < src.len() && src[end].is_ascii_digit() {
        end += 1;
    }
    let s = std::str::from_utf8(&src[start..end]).unwrap_or("");
    (end, s)
}

fn lookup_redir_after_digits(rest: &[u8]) -> Option<(Op, usize)> {
    if rest.starts_with(b">>") {
        Some((Op::DGreat, 2))
    } else if rest.starts_with(b"<<<") {
        Some((Op::TLess, 3))
    } else if rest.starts_with(b"<<-") {
        Some((Op::DLessDash, 3))
    } else if rest.starts_with(b"<<") {
        Some((Op::DLess, 2))
    } else if rest.starts_with(b"<>") {
        Some((Op::LessGreat, 2))
    } else if rest.starts_with(b"<&") {
        Some((Op::LessAmp, 2))
    } else if rest.starts_with(b">&") {
        Some((Op::GreatAmp, 2))
    } else if rest.starts_with(b">|") {
        Some((Op::GreatPipe, 2))
    } else if rest.first() == Some(&b'<') {
        Some((Op::Less, 1))
    } else if rest.first() == Some(&b'>') {
        Some((Op::Great, 1))
    } else {
        None
    }
}

fn is_word_delim(b: u8) -> bool {
    matches!(
        b,
        b' ' | b'\t' | b'\n' | b'|' | b'&' | b';' | b'(' | b')' | b'<' | b'>' | b'#'
    )
}

fn parse_dquoted_body(s: &[u8], base: usize) -> Result<Vec<WordPart>, LexError> {
    let mut parts = Vec::new();
    let mut lit = Vec::<u8>::new();
    let mut i = 0;
    while i < s.len() {
        let c = s[i];
        match c {
            b'\\' => {
                if i + 1 < s.len() {
                    let n = s[i + 1];
                    match n {
                        b'$' | b'`' | b'"' | b'\\' => {
                            lit.push(n);
                            i += 2;
                            continue;
                        }
                        b'\n' => {
                            i += 2;
                            continue;
                        }
                        _ => {
                            lit.push(b'\\');
                            i += 1;
                            continue;
                        }
                    }
                }
                lit.push(b'\\');
                i += 1;
            }
            b'$' => {
                if !lit.is_empty() {
                    parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                }
                let (part, consumed) = parse_dollar(s, i, base)?;
                parts.push(part);
                i += consumed;
            }
            b'`' => {
                if !lit.is_empty() {
                    parts.push(WordPart::Literal(BString::from(std::mem::take(&mut lit))));
                }
                let (part, consumed) = parse_backtick(s, i, base)?;
                parts.push(part);
                i += consumed;
            }
            _ => {
                lit.push(c);
                i += 1;
            }
        }
    }
    if !lit.is_empty() {
        parts.push(WordPart::Literal(BString::from(lit)));
    }
    Ok(parts)
}

fn parse_dollar(s: &[u8], i: usize, base: usize) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(s[i], b'$');
    let next = s.get(i + 1).copied();
    match next {
        Some(b'{') => parse_dollar_brace(s, i, base),
        Some(b'(') => {
            if s.get(i + 2) == Some(&b'(') {
                parse_dollar_double_paren(s, i, base)
            } else {
                parse_dollar_paren(s, i, base)
            }
        }
        Some(b'\'') => parse_dollar_single_quoted(s, i, base),
        Some(b'"') => parse_dollar_double_quoted(s, i, base),
        Some(c) if is_var_name_start(c) => {
            let mut j = i + 1;
            while j < s.len() && is_var_name_cont(s[j]) {
                j += 1;
            }
            let name = BString::from(&s[i + 1..j]);
            Ok((WordPart::DollarVar(name), j - i))
        }
        Some(c) if is_special_param(c) => {
            let name = BString::from(&s[i + 1..i + 2]);
            Ok((WordPart::DollarVar(name), 2))
        }
        Some(c) if c.is_ascii_digit() => {
            let name = BString::from(&s[i + 1..i + 2]);
            Ok((WordPart::DollarVar(name), 2))
        }
        _ => Ok((WordPart::Literal(BString::from(b"$".as_slice())), 1)),
    }
}

fn parse_dollar_brace(s: &[u8], i: usize, base: usize) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(&s[i..i + 2], b"${");
    let mut depth = 1usize;
    let body_start = i + 2;
    let mut j = body_start;
    while j < s.len() {
        match s[j] {
            b'{' => depth += 1,
            b'}' => {
                depth -= 1;
                if depth == 0 {
                    let body = BString::from(&s[body_start..j]);
                    return Ok((WordPart::DollarBrace(body), j - i + 1));
                }
            }
            b'\\' if j + 1 < s.len() => j += 1,
            b'\'' => {
                j += 1;
                while j < s.len() && s[j] != b'\'' {
                    j += 1;
                }
            }
            b'"' => {
                j += 1;
                while j < s.len() {
                    if s[j] == b'\\' && j + 1 < s.len() {
                        j += 2;
                        continue;
                    }
                    if s[j] == b'"' {
                        break;
                    }
                    j += 1;
                }
            }
            _ => {}
        }
        j += 1;
    }
    Err(LexError {
        pos: base + i,
        message: "unterminated `${...}`".into(),
    })
}

fn parse_dollar_paren(s: &[u8], i: usize, base: usize) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(&s[i..i + 2], b"$(");
    let body_start = i + 2;
    let body_end = find_matching_paren(s, body_start, base)?;
    let body = &s[body_start..body_end];
    let stmts =
        crate::parse::parse_inner_script(body, base + body_start).map_err(|e| LexError {
            pos: e.pos,
            message: format!("$(...): {}", e.message),
        })?;
    Ok((WordPart::DollarParen(stmts), body_end + 1 - i))
}

fn parse_dollar_double_paren(
    s: &[u8],
    i: usize,
    base: usize,
) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(&s[i..i + 3], b"$((");
    let body_start = i + 3;
    let mut depth = 1i32;
    let mut j = body_start;
    while j < s.len() {
        match s[j] {
            b'(' => depth += 1,
            b')' => {
                if s.get(j + 1) == Some(&b')') && depth == 1 {
                    let body = BString::from(&s[body_start..j]);
                    return Ok((WordPart::DollarDoubleParen(body), j + 2 - i));
                }
                depth -= 1;
            }
            b'\\' if j + 1 < s.len() => j += 1,
            _ => {}
        }
        j += 1;
    }
    Err(LexError {
        pos: base + i,
        message: "unterminated `$((...))`".into(),
    })
}

fn parse_dollar_single_quoted(
    s: &[u8],
    i: usize,
    base: usize,
) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(&s[i..i + 2], b"$'");
    let body_start = i + 2;
    let mut buf = Vec::new();
    let mut j = body_start;
    while j < s.len() {
        match s[j] {
            b'\'' => return Ok((WordPart::AnsiCQuoted(BString::from(buf)), j + 1 - i)),
            b'\\' if j + 1 < s.len() => {
                let n = s[j + 1];
                match n {
                    b'n' => buf.push(b'\n'),
                    b't' => buf.push(b'\t'),
                    b'r' => buf.push(b'\r'),
                    b'\\' => buf.push(b'\\'),
                    b'\'' => buf.push(b'\''),
                    b'"' => buf.push(b'"'),
                    b'0' => buf.push(0),
                    b'a' => buf.push(7),
                    b'b' => buf.push(8),
                    b'e' | b'E' => buf.push(0x1b),
                    b'f' => buf.push(12),
                    b'v' => buf.push(11),
                    other => {
                        buf.push(b'\\');
                        buf.push(other);
                    }
                }
                j += 2;
                continue;
            }
            c => {
                buf.push(c);
                j += 1;
            }
        }
    }
    Err(LexError {
        pos: base + i,
        message: "unterminated `$'...'`".into(),
    })
}

fn parse_dollar_double_quoted(
    s: &[u8],
    i: usize,
    base: usize,
) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(&s[i..i + 2], b"$\"");
    let body_start = i + 2;
    let mut j = body_start;
    while j < s.len() {
        match s[j] {
            b'"' => {
                let body = BString::from(&s[body_start..j]);
                return Ok((WordPart::LocaleQuoted(body), j + 1 - i));
            }
            b'\\' if j + 1 < s.len() => j += 2,
            _ => j += 1,
        }
    }
    Err(LexError {
        pos: base + i,
        message: "unterminated `$\"...\"`".into(),
    })
}

fn parse_backtick(s: &[u8], i: usize, base: usize) -> Result<(WordPart, usize), LexError> {
    debug_assert_eq!(s[i], b'`');
    let body_start = i + 1;
    let mut buf = Vec::new();
    let mut j = body_start;
    while j < s.len() {
        match s[j] {
            b'`' => {
                let stmts =
                    crate::parse::parse_inner_script(&buf, base + body_start).map_err(|e| {
                        LexError {
                            pos: e.pos,
                            message: format!("`...`: {}", e.message),
                        }
                    })?;
                return Ok((WordPart::Backtick(stmts), j + 1 - i));
            }
            b'\\' if j + 1 < s.len() => {
                let n = s[j + 1];
                match n {
                    b'$' | b'`' | b'\\' => {
                        buf.push(n);
                        j += 2;
                        continue;
                    }
                    _ => {
                        buf.push(b'\\');
                        buf.push(n);
                        j += 2;
                        continue;
                    }
                }
            }
            c => {
                buf.push(c);
                j += 1;
            }
        }
    }
    Err(LexError {
        pos: base + i,
        message: "unterminated `\\`...\\``".into(),
    })
}

fn find_matching_paren(s: &[u8], from: usize, base: usize) -> Result<usize, LexError> {
    let mut depth = 1i32;
    let mut j = from;
    while j < s.len() {
        match s[j] {
            b'(' => depth += 1,
            b')' => {
                depth -= 1;
                if depth == 0 {
                    return Ok(j);
                }
            }
            b'\\' if j + 1 < s.len() => j += 1,
            b'\'' => {
                j += 1;
                while j < s.len() && s[j] != b'\'' {
                    j += 1;
                }
            }
            b'"' => {
                j += 1;
                while j < s.len() {
                    if s[j] == b'\\' && j + 1 < s.len() {
                        j += 2;
                        continue;
                    }
                    if s[j] == b'"' {
                        break;
                    }
                    j += 1;
                }
            }
            _ => {}
        }
        j += 1;
    }
    Err(LexError {
        pos: base + from - 1,
        message: "unterminated parenthesised group".into(),
    })
}

fn is_var_name_start(c: u8) -> bool {
    c.is_ascii_alphabetic() || c == b'_'
}

fn is_var_name_cont(c: u8) -> bool {
    c.is_ascii_alphanumeric() || c == b'_'
}

fn is_special_param(c: u8) -> bool {
    matches!(c, b'?' | b'!' | b'$' | b'@' | b'*' | b'#' | b'-' | b'0')
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lex_all(src: &[u8]) -> Vec<Token> {
        let mut l = Lexer::new(src);
        let mut out = Vec::new();
        loop {
            let t = l.next_token().expect("lex");
            let done = matches!(t, Token::Eof { .. });
            out.push(t);
            if done {
                break;
            }
        }
        out
    }

    #[test]
    fn simple_command() {
        let toks = lex_all(b"echo hello");
        assert!(matches!(toks[0], Token::Word { .. }));
        assert!(matches!(toks[1], Token::Word { .. }));
        assert!(matches!(toks[2], Token::Eof { .. }));
    }

    #[test]
    fn pipe_and_semi() {
        let toks = lex_all(b"a | b ; c");
        let kinds: Vec<_> = toks
            .iter()
            .map(|t| match t {
                Token::Word { .. } => "W",
                Token::Op { op: Op::Pipe, .. } => "|",
                Token::Op { op: Op::Semi, .. } => ";",
                Token::Eof { .. } => "$",
                _ => "?",
            })
            .collect();
        assert_eq!(kinds, ["W", "|", "W", ";", "W", "$"]);
    }

    #[test]
    fn fd_prefix_then_op() {
        let toks = lex_all(b"echo hi 2>&1");
        let mut iter = toks.iter();
        assert!(matches!(iter.next(), Some(Token::Word { .. })));
        assert!(matches!(iter.next(), Some(Token::Word { .. })));
        match iter.next() {
            Some(Token::FdPrefix { fd: 2, .. }) => {}
            other => panic!("expected FdPrefix(2), got {other:?}"),
        }
        match iter.next() {
            Some(Token::Op {
                op: Op::GreatAmp, ..
            }) => {}
            other => panic!("expected GreatAmp, got {other:?}"),
        }
    }

    #[test]
    fn double_quote_with_var() {
        let toks = lex_all(b"echo \"hi $name\"");
        let word = match &toks[1] {
            Token::Word { word, .. } => word,
            _ => panic!(),
        };
        assert_eq!(word.parts.len(), 1);
        match &word.parts[0] {
            WordPart::DoubleQuoted(parts) => {
                assert_eq!(parts.len(), 2);
                assert!(matches!(parts[0], WordPart::Literal(_)));
                assert!(matches!(parts[1], WordPart::DollarVar(_)));
            }
            _ => panic!(),
        }
    }

    #[test]
    fn comment_skipped() {
        let toks = lex_all(b"echo hi # trailing\n");
        // echo, hi, newline, eof
        assert_eq!(toks.len(), 4);
        assert!(matches!(toks[0], Token::Word { .. }));
        assert!(matches!(toks[1], Token::Word { .. }));
        assert!(matches!(toks[2], Token::Newline { .. }));
        assert!(matches!(toks[3], Token::Eof { .. }));
    }

    #[test]
    fn line_continuation() {
        let toks = lex_all(b"echo \\\n  hi");
        assert!(matches!(toks[0], Token::Word { .. }));
        assert!(matches!(toks[1], Token::Word { .. }));
        assert!(matches!(toks[2], Token::Eof { .. }));
    }

    #[test]
    fn single_quoted_raw() {
        let toks = lex_all(b"echo 'a\\nb$c'");
        let word = match &toks[1] {
            Token::Word { word, .. } => word,
            _ => panic!(),
        };
        match &word.parts[0] {
            WordPart::SingleQuoted(s) => assert_eq!(s.as_slice(), b"a\\nb$c"),
            _ => panic!(),
        }
    }

    #[test]
    fn dollar_paren_inside_word() {
        let toks = lex_all(b"echo $(date)");
        let word = match &toks[1] {
            Token::Word { word, .. } => word,
            _ => panic!(),
        };
        assert!(matches!(&word.parts[0], WordPart::DollarParen(_)));
    }
}
