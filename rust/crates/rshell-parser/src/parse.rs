//! Recursive-descent parser. Consumes the token stream produced by
//! `rshell_parser::lex` and builds the AST defined in
//! `rshell_parser::ast`.
//!
//! Phase 2 scope: the core grammar — simple commands, pipelines,
//! and-or lists, redirections (with here-docs), if / while / until / for /
//! case / function / brace-group / subshell. `[[ ... ]]` and `(( ... ))`
//! are recognised but their bodies are kept as raw bytes (Phase 3 hooks
//! evaluation in).

use std::fmt;

use bstr::BString;

use crate::ast::*;
use crate::lex::{LexError, Lexer, Op, Span, Token};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParseError {
    pub pos: usize,
    pub message: String,
}

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "parse error at byte {}: {}", self.pos, self.message)
    }
}

impl std::error::Error for ParseError {}

impl From<LexError> for ParseError {
    fn from(e: LexError) -> Self {
        Self {
            pos: e.pos,
            message: e.message,
        }
    }
}

/// Parse a shell script.
pub fn parse_script(src: &[u8]) -> Result<Script, ParseError> {
    let stmts = parse_inner_script(src, 0)?;
    Ok(Script { stmts })
}

/// Parse a sequence of statements. Used at the top level and for nested
/// `$(...)` / backtick bodies. `base` shifts error positions so they
/// remain meaningful when reporting from a nested parse.
pub fn parse_inner_script(src: &[u8], base: usize) -> Result<Vec<Stmt>, ParseError> {
    let lex = Lexer::new(src);
    let mut p = Parser::new(lex, base);
    let mut stmts = p.parse_complete_commands()?;
    p.expect_eof()?;
    // Attach deferred here-doc bodies in document order.
    let mut bodies = std::mem::take(&mut p.pending_bodies).into_iter();
    attach_heredoc_bodies(&mut stmts, &mut bodies);
    Ok(stmts)
}

fn attach_heredoc_bodies(stmts: &mut [Stmt], bodies: &mut std::vec::IntoIter<HereDocBody>) {
    for s in stmts {
        attach_in_redirs(&mut s.redirs, bodies);
        match &mut s.command {
            Command::Simple(c) => attach_in_redirs(&mut c.redirs, bodies),
            Command::Pipeline(p) => attach_heredoc_bodies(&mut p.cmds, bodies),
            Command::AndOr(ao) => {
                attach_heredoc_bodies(std::slice::from_mut(&mut ao.left), bodies);
                attach_heredoc_bodies(std::slice::from_mut(&mut ao.right), bodies);
            }
            Command::Subshell(s) | Command::BraceGroup(s) => attach_heredoc_bodies(s, bodies),
            Command::If(c) => {
                attach_heredoc_bodies(&mut c.cond, bodies);
                attach_heredoc_bodies(&mut c.then, bodies);
                for elif in &mut c.elifs {
                    attach_heredoc_bodies(&mut elif.cond, bodies);
                    attach_heredoc_bodies(&mut elif.then, bodies);
                }
                if let Some(e) = &mut c.else_branch {
                    attach_heredoc_bodies(e, bodies);
                }
            }
            Command::While(c) => {
                attach_heredoc_bodies(&mut c.cond, bodies);
                attach_heredoc_bodies(&mut c.body, bodies);
            }
            Command::Until(c) => {
                attach_heredoc_bodies(&mut c.cond, bodies);
                attach_heredoc_bodies(&mut c.body, bodies);
            }
            Command::For(c) => attach_heredoc_bodies(&mut c.body, bodies),
            Command::Case(c) => {
                for item in &mut c.items {
                    attach_heredoc_bodies(&mut item.body, bodies);
                }
            }
            Command::Function(f) => {
                attach_heredoc_bodies(std::slice::from_mut(&mut f.body), bodies)
            }
            Command::DoubleBracket(_) | Command::Arith(_) => {}
        }
    }
}

fn attach_in_redirs(redirs: &mut [Redir], bodies: &mut std::vec::IntoIter<HereDocBody>) {
    for r in redirs {
        if matches!(r.op, RedirOp::HereDoc | RedirOp::HereDocStrip)
            && r.heredoc_body.is_none()
            && let Some(body) = bodies.next()
        {
            r.heredoc_body = Some(body);
        }
    }
}

struct Parser<'a> {
    lex: Lexer<'a>,
    /// Look-ahead queue (front-to-back). `bump` pops from the front;
    /// `peek_n(0)` gets the front.
    ahead: std::collections::VecDeque<Token>,
    /// Here-doc bodies queued by the lexer, drained during token reads.
    /// They are attached to redirections in a post-parse pass so that
    /// constructs like `cat <<EOF | grep x ... \n body \n EOF` work.
    pending_bodies: Vec<HereDocBody>,
    base: usize,
}

impl<'a> Parser<'a> {
    fn new(lex: Lexer<'a>, base: usize) -> Self {
        Self {
            lex,
            ahead: std::collections::VecDeque::new(),
            pending_bodies: Vec::new(),
            base,
        }
    }

    fn ensure_ahead(&mut self, n: usize) -> Result<(), ParseError> {
        while self.ahead.len() <= n {
            let t = self.lex.next_token()?;
            // Silently siphon here-doc bodies into pending_bodies; the
            // post-parse pass attaches them to here-doc redirs in order.
            match t {
                Token::HereDocBody { body, .. } => self.pending_bodies.push(body),
                t => self.ahead.push_back(t),
            }
        }
        Ok(())
    }

    fn peek(&mut self) -> Result<&Token, ParseError> {
        self.ensure_ahead(0)?;
        Ok(self.ahead.front().unwrap())
    }

    fn peek_n(&mut self, n: usize) -> Result<&Token, ParseError> {
        self.ensure_ahead(n)?;
        Ok(self.ahead.get(n).unwrap())
    }

    fn bump(&mut self) -> Result<Token, ParseError> {
        if let Some(t) = self.ahead.pop_front() {
            Ok(t)
        } else {
            Ok(self.lex.next_token()?)
        }
    }

    fn expect_eof(&mut self) -> Result<(), ParseError> {
        match self.peek()? {
            Token::Eof { .. } => Ok(()),
            other => {
                let pos = token_pos(other);
                let kind = token_kind(other);
                Err(self.err(pos, format!("unexpected token: {kind}")))
            }
        }
    }

    fn err(&self, pos: usize, message: String) -> ParseError {
        ParseError {
            pos: self.base + pos,
            message,
        }
    }

    /// Skip newlines and semicolons (statement separators), used between
    /// keywords like `then`, `do`, `else`, etc.
    fn skip_seps(&mut self) -> Result<(), ParseError> {
        loop {
            match self.peek()? {
                Token::Newline { .. } => {
                    self.bump()?;
                }
                Token::Op { op: Op::Semi, .. } => {
                    self.bump()?;
                }
                _ => return Ok(()),
            }
        }
    }

    /// Parse a sequence of top-level statements.
    fn parse_complete_commands(&mut self) -> Result<Vec<Stmt>, ParseError> {
        let mut stmts = Vec::new();
        loop {
            self.skip_seps()?;
            if let Token::Eof { .. } = self.peek()? {
                return Ok(stmts);
            }
            let stmt = self.parse_and_or()?;
            stmts.push(stmt);
            // Statement terminator: `;`, `&`, newline, or EOF.
            match self.peek()? {
                Token::Op { op: Op::Semi, .. } | Token::Newline { .. } => {
                    self.bump()?;
                }
                Token::Op { op: Op::Amp, .. } => {
                    self.bump()?;
                    if let Some(last) = stmts.last_mut() {
                        last.background = true;
                    }
                }
                Token::Eof { .. } => return Ok(stmts),
                // Closing tokens of compound commands (handled by callers).
                Token::Op { op: Op::RParen, .. } => return Ok(stmts),
                Token::Word { word, .. } if is_keyword(word, FOLLOWERS) => return Ok(stmts),
                other => {
                    let pos = token_pos(other);
                    let kind = token_kind(other);
                    return Err(self.err(pos, format!("expected `;`, `&`, or newline; got {kind}")));
                }
            }
        }
    }

    /// `and_or := pipeline (('&&' | '||') newlines? pipeline)*`
    fn parse_and_or(&mut self) -> Result<Stmt, ParseError> {
        let mut left = self.parse_pipeline()?;
        loop {
            let op = match self.peek()? {
                Token::Op { op: Op::AndAnd, .. } => AndOrOp::AndAnd,
                Token::Op { op: Op::OrOr, .. } => AndOrOp::OrOr,
                _ => return Ok(left),
            };
            self.bump()?;
            // Optional newlines after `&&` / `||`.
            while matches!(self.peek()?, Token::Newline { .. }) {
                self.bump()?;
            }
            let right = self.parse_pipeline()?;
            left = Stmt {
                command: Command::AndOr(AndOr {
                    left: Box::new(left),
                    op,
                    right: Box::new(right),
                }),
                negated: false,
                background: false,
                redirs: Vec::new(),
            };
        }
    }

    /// `pipeline := ['!'] cmd (('|' | '|&') newlines? cmd)*`
    fn parse_pipeline(&mut self) -> Result<Stmt, ParseError> {
        let negated = match self.peek()? {
            Token::Word { word, .. } if word_eq(word, b"!") => {
                self.bump()?;
                true
            }
            _ => false,
        };
        let first = self.parse_command_with_redirs()?;
        let mut cmds = vec![first];
        let mut all = false;
        loop {
            match self.peek()? {
                Token::Op { op: Op::Pipe, .. } => {
                    self.bump()?;
                }
                Token::Op {
                    op: Op::PipeAmp, ..
                } => {
                    self.bump()?;
                    all = true;
                }
                _ => break,
            }
            while matches!(self.peek()?, Token::Newline { .. }) {
                self.bump()?;
            }
            let next = self.parse_command_with_redirs()?;
            cmds.push(next);
        }
        if cmds.len() == 1 && !negated {
            return Ok(cmds.pop().unwrap());
        }
        Ok(Stmt {
            command: Command::Pipeline(Pipeline { cmds, all }),
            negated,
            background: false,
            redirs: Vec::new(),
        })
    }

    /// Parse a single command (simple or compound) plus any trailing
    /// redirections that attach to it.
    fn parse_command_with_redirs(&mut self) -> Result<Stmt, ParseError> {
        // Detect compound-command openers.
        if let Some(stmt) = self.try_compound_command()? {
            return Ok(stmt);
        }
        // Otherwise it's a simple command.
        let cmd = self.parse_simple_command()?;
        Ok(Stmt {
            command: Command::Simple(cmd),
            negated: false,
            background: false,
            redirs: Vec::new(),
        })
    }

    fn try_compound_command(&mut self) -> Result<Option<Stmt>, ParseError> {
        // Identify the kind of compound command without consuming.
        let kind = match self.peek()? {
            Token::Op { op: Op::LParen, .. } => Some(CompoundKind::Subshell),
            Token::Word { word, .. } if word_eq(word, b"{") => Some(CompoundKind::BraceGroup),
            Token::Word { word, .. } if word_eq(word, b"if") => Some(CompoundKind::If),
            Token::Word { word, .. } if word_eq(word, b"while") => Some(CompoundKind::While),
            Token::Word { word, .. } if word_eq(word, b"until") => Some(CompoundKind::Until),
            Token::Word { word, .. } if word_eq(word, b"for") => Some(CompoundKind::For),
            Token::Word { word, .. } if word_eq(word, b"case") => Some(CompoundKind::Case),
            Token::Word { word, .. } if word_eq(word, b"function") => Some(CompoundKind::Function),
            Token::Word { word, .. } if word_eq(word, b"[[") => Some(CompoundKind::DoubleBracket),
            _ => None,
        };
        let Some(kind) = kind else {
            // Detect `name() { ... }` style function definition: a single
            // word immediately followed by `()`.
            if let Some(stmt) = self.try_paren_function()? {
                return Ok(Some(stmt));
            }
            return Ok(None);
        };

        let cmd = match kind {
            CompoundKind::Subshell => self.parse_subshell()?,
            CompoundKind::BraceGroup => self.parse_brace_group()?,
            CompoundKind::If => self.parse_if()?,
            CompoundKind::While => {
                self.bump()?;
                let cond = self.parse_compound_list_until(&[b"do"])?;
                self.expect_keyword(b"do")?;
                let body = self.parse_compound_list_until(&[b"done"])?;
                self.expect_keyword(b"done")?;
                Command::While(WhileCmd { cond, body })
            }
            CompoundKind::Until => {
                self.bump()?;
                let cond = self.parse_compound_list_until(&[b"do"])?;
                self.expect_keyword(b"do")?;
                let body = self.parse_compound_list_until(&[b"done"])?;
                self.expect_keyword(b"done")?;
                Command::Until(UntilCmd { cond, body })
            }
            CompoundKind::For => self.parse_for()?,
            CompoundKind::Case => self.parse_case()?,
            CompoundKind::Function => self.parse_function_keyword()?,
            CompoundKind::DoubleBracket => self.parse_double_bracket()?,
        };

        let redirs = self.parse_redirs()?;
        Ok(Some(Stmt {
            command: cmd,
            negated: false,
            background: false,
            redirs,
        }))
    }

    fn try_paren_function(&mut self) -> Result<Option<Stmt>, ParseError> {
        // Lookahead: a literal-only word followed by `(` then `)`.
        match self.peek()? {
            Token::Word { word, .. } if word_is_simple_name(word) => {}
            _ => return Ok(None),
        }
        // Peek ahead 2 more tokens without consuming.
        let is_paren_open = matches!(self.peek_n(1)?, Token::Op { op: Op::LParen, .. });
        if !is_paren_open {
            return Ok(None);
        }
        let is_paren_close = matches!(self.peek_n(2)?, Token::Op { op: Op::RParen, .. });
        if !is_paren_close {
            return Ok(None);
        }
        // Commit: consume name, `(`, `)`.
        let name_tok = self.bump()?;
        self.bump()?; // (
        self.bump()?; // )
        let name = match name_tok {
            Token::Word { word, .. } => word_to_name(word)
                .ok_or_else(|| self.err(0, "function name must be a literal".into()))?,
            _ => unreachable!(),
        };
        while matches!(self.peek()?, Token::Newline { .. }) {
            self.bump()?;
        }
        let body = self.parse_command_with_redirs()?;
        Ok(Some(Stmt {
            command: Command::Function(FunctionDef {
                name,
                body: Box::new(body),
            }),
            negated: false,
            background: false,
            redirs: Vec::new(),
        }))
    }

    fn parse_subshell(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // (
        let stmts = self.parse_compound_list_until_op(Op::RParen)?;
        match self.peek()? {
            Token::Op { op: Op::RParen, .. } => {
                self.bump()?;
                Ok(Command::Subshell(stmts))
            }
            other => {
                let pos = token_pos(other);
                Err(self.err(pos, "expected `)`".into()))
            }
        }
    }

    fn parse_brace_group(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `{` (a literal word, not an op)
        // A body terminated by a `}` reserved word.
        let stmts = self.parse_compound_list_until(&[b"}"])?;
        self.expect_keyword(b"}")?;
        Ok(Command::BraceGroup(stmts))
    }

    fn parse_if(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `if`
        let cond = self.parse_compound_list_until(&[b"then"])?;
        self.expect_keyword(b"then")?;
        let then = self.parse_compound_list_until(&[b"elif", b"else", b"fi"])?;
        let mut elifs = Vec::new();
        loop {
            match self.peek()? {
                Token::Word { word, .. } if word_eq(word, b"elif") => {
                    self.bump()?;
                    let cond = self.parse_compound_list_until(&[b"then"])?;
                    self.expect_keyword(b"then")?;
                    let body = self.parse_compound_list_until(&[b"elif", b"else", b"fi"])?;
                    elifs.push(ElifBranch { cond, then: body });
                }
                _ => break,
            }
        }
        let else_branch = match self.peek()? {
            Token::Word { word, .. } if word_eq(word, b"else") => {
                self.bump()?;
                Some(self.parse_compound_list_until(&[b"fi"])?)
            }
            _ => None,
        };
        self.expect_keyword(b"fi")?;
        Ok(Command::If(IfCmd {
            cond,
            then,
            elifs,
            else_branch,
        }))
    }

    fn parse_for(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `for`
        // C-style: `for (( init; cond; update )); do ... done`. Detect by
        // two adjacent `(` tokens after the `for` keyword.
        if let Token::Op {
            op: Op::LParen,
            span: lp1,
        } = self.peek()?
        {
            let lp1_end = lp1.end;
            if let Token::Op {
                op: Op::LParen,
                span: lp2,
            } = self.peek_n(1)?
                && lp2.start == lp1_end
            {
                self.bump()?; // (
                self.bump()?; // (
                // Read raw bytes until matching `))`. Track depth on `(` /
                // `)` operators only; words are appended verbatim.
                let mut depth = 1i32; // we're inside one extra `(`
                let mut buf = Vec::<u8>::new();
                let mut last_end = lp1_end + 1; // after second `(`
                loop {
                    // Snapshot the next token's kind+span so we can also
                    // peek the one after without holding the first borrow.
                    let next_kind_span = match self.peek()? {
                        Token::Op { op, span } => Some((Some(*op), *span)),
                        Token::Word { span, .. }
                        | Token::FdPrefix { span, .. }
                        | Token::Newline { span, .. }
                        | Token::HereDocBody { span, .. } => Some((None, *span)),
                        Token::Eof { .. } => None,
                    };
                    match next_kind_span {
                        Some((Some(Op::RParen), span)) => {
                            // `))` closes the C-style header.
                            let next2_adjacent = matches!(
                                self.peek_n(1)?,
                                Token::Op { op: Op::RParen, span: span2 }
                                    if span2.start == span.end
                            );
                            if next2_adjacent && depth == 1 {
                                self.bump()?;
                                self.bump()?;
                                break;
                            }
                            depth -= 1;
                            if span.start > last_end {
                                buf.push(b' ');
                            }
                            buf.push(b')');
                            last_end = span.end;
                            self.bump()?;
                        }
                        Some((Some(Op::LParen), span)) => {
                            depth += 1;
                            if span.start > last_end {
                                buf.push(b' ');
                            }
                            buf.push(b'(');
                            last_end = span.end;
                            self.bump()?;
                        }
                        None => {
                            return Err(self.err(last_end, "unterminated `for ((...))`".into()));
                        }
                        Some((_, span)) => {
                            if span.start > last_end {
                                buf.push(b' ');
                            }
                            let t = self.bump()?;
                            push_token_text_raw(&mut buf, &t);
                            last_end = span.end;
                        }
                    }
                }
                self.skip_seps()?;
                self.expect_keyword(b"do")?;
                let body = self.parse_compound_list_until(&[b"done"])?;
                self.expect_keyword(b"done")?;
                return Ok(Command::For(ForCmd {
                    var: BString::default(),
                    items: None,
                    c_style: Some(BString::from(buf)),
                    body,
                }));
            }
        }
        // Variable name.
        let var = match self.bump()? {
            Token::Word { word, .. } => word_to_name(word)
                .ok_or_else(|| self.err(0, "for-loop variable must be a simple name".into()))?,
            other => {
                let pos = token_pos(&other);
                return Err(self.err(pos, "expected variable name after `for`".into()));
            }
        };
        // Optional `in <words>` — bash also accepts it on a separate line.
        // Skip newlines between var and `in` per POSIX.
        while matches!(self.peek()?, Token::Newline { .. }) {
            self.bump()?;
        }
        let items = if let Token::Word { word, .. } = self.peek()?
            && word_eq(word, b"in")
        {
            self.bump()?;
            let mut items = Vec::new();
            loop {
                match self.peek()? {
                    Token::Word { .. } => {
                        let Token::Word { word, .. } = self.bump()? else {
                            unreachable!()
                        };
                        items.push(word);
                    }
                    Token::Op { op: Op::Semi, .. } => {
                        self.bump()?;
                        break;
                    }
                    Token::Newline { .. } => {
                        self.bump()?;
                        break;
                    }
                    other => {
                        let pos = token_pos(other);
                        let kind = token_kind(other);
                        return Err(self.err(pos, format!("unexpected {kind} in `for` items")));
                    }
                }
            }
            Some(items)
        } else {
            None
        };
        self.skip_seps()?;
        self.expect_keyword(b"do")?;
        let body = self.parse_compound_list_until(&[b"done"])?;
        self.expect_keyword(b"done")?;
        Ok(Command::For(ForCmd {
            var,
            items,
            c_style: None,
            body,
        }))
    }

    fn parse_case(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `case`
        let word = match self.bump()? {
            Token::Word { word, .. } => word,
            other => {
                let pos = token_pos(&other);
                return Err(self.err(pos, "expected word after `case`".into()));
            }
        };
        self.skip_seps()?;
        self.expect_keyword(b"in")?;
        self.skip_seps()?;
        let mut items = Vec::new();
        loop {
            // End of case.
            if let Token::Word { word, .. } = self.peek()?
                && word_eq(word, b"esac")
            {
                self.bump()?;
                break;
            }
            // Optional leading `(`.
            if matches!(self.peek()?, Token::Op { op: Op::LParen, .. }) {
                self.bump()?;
            }
            // One or more patterns separated by `|`.
            let mut patterns = Vec::new();
            loop {
                let Token::Word { word, .. } = self.bump()? else {
                    return Err(self.err(0, "expected pattern in case item".into()));
                };
                patterns.push(word);
                match self.peek()? {
                    Token::Op { op: Op::Pipe, .. } => {
                        self.bump()?;
                    }
                    _ => break,
                }
            }
            // Closing `)`.
            match self.peek()? {
                Token::Op { op: Op::RParen, .. } => {
                    self.bump()?;
                }
                other => {
                    let pos = token_pos(other);
                    return Err(self.err(pos, "expected `)` after case pattern".into()));
                }
            }
            // Body until ;; / ;& / ;;& or `esac`.
            let body = self.parse_case_body()?;
            let term = match self.peek()? {
                Token::Op {
                    op: Op::SemiSemi, ..
                } => {
                    self.bump()?;
                    CaseTerm::Break
                }
                Token::Op {
                    op: Op::SemiAmp, ..
                } => {
                    self.bump()?;
                    CaseTerm::Fallthrough
                }
                Token::Op {
                    op: Op::SemiSemiAmp,
                    ..
                } => {
                    self.bump()?;
                    CaseTerm::RetestNext
                }
                Token::Word { word, .. } if word_eq(word, b"esac") => CaseTerm::Break,
                other => {
                    let pos = token_pos(other);
                    let kind = token_kind(other);
                    return Err(self.err(pos, format!("expected `;;` or `esac`, got {kind}")));
                }
            };
            items.push(CaseItem {
                patterns,
                body,
                term,
            });
            self.skip_seps()?;
        }
        Ok(Command::Case(CaseCmd { word, items }))
    }

    /// Parse the body of a single `case` item, terminated by `;;`, `;&`,
    /// `;;&`, or `esac`. Statements within may be separated by newlines
    /// or `;`.
    fn parse_case_body(&mut self) -> Result<Vec<Stmt>, ParseError> {
        let mut stmts = Vec::new();
        loop {
            self.skip_seps()?;
            match self.peek()? {
                Token::Op {
                    op: Op::SemiSemi | Op::SemiAmp | Op::SemiSemiAmp,
                    ..
                } => return Ok(stmts),
                Token::Word { word, .. } if word_eq(word, b"esac") => return Ok(stmts),
                Token::Eof { .. } => return Ok(stmts),
                _ => {}
            }
            let s = self.parse_and_or()?;
            stmts.push(s);
            match self.peek()? {
                Token::Op { op: Op::Semi, .. } | Token::Newline { .. } => {
                    self.bump()?;
                }
                Token::Op { op: Op::Amp, .. } => {
                    self.bump()?;
                    if let Some(last) = stmts.last_mut() {
                        last.background = true;
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_function_keyword(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `function`
        let name = match self.bump()? {
            Token::Word { word, .. } => word_to_name(word)
                .ok_or_else(|| self.err(0, "function name must be a literal".into()))?,
            other => {
                let pos = token_pos(&other);
                return Err(self.err(pos, "expected function name".into()));
            }
        };
        // Optional `()`.
        if matches!(self.peek()?, Token::Op { op: Op::LParen, .. }) {
            self.bump()?;
            match self.peek()? {
                Token::Op { op: Op::RParen, .. } => {
                    self.bump()?;
                }
                other => {
                    let pos = token_pos(other);
                    return Err(self.err(pos, "expected `)` after `function name (`".into()));
                }
            }
        }
        while matches!(self.peek()?, Token::Newline { .. }) {
            self.bump()?;
        }
        let body = self.parse_command_with_redirs()?;
        Ok(Command::Function(FunctionDef {
            name,
            body: Box::new(body),
        }))
    }

    fn parse_double_bracket(&mut self) -> Result<Command, ParseError> {
        self.bump()?; // `[[`
        // Capture words until we hit `]]`.
        let mut buf = Vec::<u8>::new();
        loop {
            match self.peek()? {
                Token::Word { word, .. } if word_eq(word, b"]]") => {
                    self.bump()?;
                    return Ok(Command::DoubleBracket(BString::from(buf)));
                }
                Token::Eof { .. } => return Err(self.err(0, "unterminated `[[ ... ]]`".into())),
                _ => {
                    let t = self.bump()?;
                    push_token_text(&mut buf, &t);
                }
            }
        }
    }

    fn parse_compound_list_until(
        &mut self,
        terminators: &[&[u8]],
    ) -> Result<Vec<Stmt>, ParseError> {
        let mut stmts = Vec::new();
        loop {
            self.skip_seps()?;
            match self.peek()? {
                Token::Eof { .. } => return Ok(stmts),
                Token::Word { word, .. } if terminators.iter().any(|t| word_eq(word, t)) => {
                    return Ok(stmts);
                }
                _ => {}
            }
            let s = self.parse_and_or()?;
            stmts.push(s);
            match self.peek()? {
                Token::Op { op: Op::Semi, .. } | Token::Newline { .. } => {
                    self.bump()?;
                }
                Token::Op { op: Op::Amp, .. } => {
                    self.bump()?;
                    if let Some(last) = stmts.last_mut() {
                        last.background = true;
                    }
                }
                _ => {}
            }
        }
    }

    fn parse_compound_list_until_op(&mut self, end: Op) -> Result<Vec<Stmt>, ParseError> {
        let mut stmts = Vec::new();
        loop {
            self.skip_seps()?;
            match self.peek()? {
                Token::Eof { .. } => return Ok(stmts),
                Token::Op { op, .. } if *op == end => return Ok(stmts),
                _ => {}
            }
            let s = self.parse_and_or()?;
            stmts.push(s);
            match self.peek()? {
                Token::Op { op: Op::Semi, .. } | Token::Newline { .. } => {
                    self.bump()?;
                }
                Token::Op { op: Op::Amp, .. } => {
                    self.bump()?;
                    if let Some(last) = stmts.last_mut() {
                        last.background = true;
                    }
                }
                _ => {}
            }
        }
    }

    fn expect_keyword(&mut self, kw: &[u8]) -> Result<(), ParseError> {
        match self.peek()? {
            Token::Word { word, .. } if word_eq(word, kw) => {
                self.bump()?;
                Ok(())
            }
            other => {
                let pos = token_pos(other);
                let kind = token_kind(other);
                let kw_str = std::str::from_utf8(kw).unwrap_or("?");
                Err(self.err(pos, format!("expected `{kw_str}`, got {kind}")))
            }
        }
    }

    fn parse_simple_command(&mut self) -> Result<SimpleCmd, ParseError> {
        let mut assigns = Vec::new();
        let mut words = Vec::new();
        let mut redirs = Vec::new();
        // Leading assignments and redirections.
        loop {
            // Handle FdPrefix + Op (redirection with explicit fd).
            if let Token::FdPrefix { fd, .. } = self.peek()? {
                let fd = *fd;
                self.bump()?;
                let r = self.parse_redir(Some(fd))?;
                redirs.push(r);
                continue;
            }
            if let Token::Op { op, .. } = self.peek()?
                && is_redir_op(*op)
            {
                let r = self.parse_redir(None)?;
                redirs.push(r);
                continue;
            }
            // Stop on terminators.
            if matches!(
                self.peek()?,
                Token::Newline { .. }
                    | Token::Op {
                        op: Op::Semi
                            | Op::Amp
                            | Op::AndAnd
                            | Op::OrOr
                            | Op::Pipe
                            | Op::PipeAmp
                            | Op::RParen
                            | Op::SemiSemi
                            | Op::SemiAmp
                            | Op::SemiSemiAmp,
                        ..
                    }
                    | Token::Eof { .. }
            ) {
                break;
            }
            // A word — maybe an assignment, maybe an argument.
            let Token::Word { word, .. } = self.peek()? else {
                let other = self.peek()?.clone();
                let pos = token_pos(&other);
                return Err(self.err(pos, format!("unexpected {} in command", token_kind(&other))));
            };
            // Assignment? Only at the start of the simple command, before
            // any non-assignment word.
            if words.is_empty()
                && let Some(mut a) = word_as_assignment(word)
            {
                // Capture the word's span end for adjacency check.
                let word_end = match self.peek()? {
                    Token::Word { span, .. } => span.end,
                    _ => unreachable!(),
                };
                self.bump()?;
                // Array form: `name=(...)` — empty value followed by an
                // adjacent `(`. Consume up to the matching `)` and store
                // the inner bytes.
                if a.value.parts.is_empty()
                    && let Token::Op {
                        op: Op::LParen,
                        span,
                    } = self.peek()?
                    && span.start == word_end
                {
                    let lp_end = span.end;
                    self.bump()?; // (
                    let mut depth = 1usize;
                    let mut buf = Vec::<u8>::new();
                    let mut last_end = lp_end;
                    loop {
                        match self.peek()? {
                            Token::Op {
                                op: Op::LParen,
                                span,
                            } => {
                                depth += 1;
                                if span.start > last_end {
                                    buf.push(b' ');
                                }
                                buf.push(b'(');
                                last_end = span.end;
                                self.bump()?;
                            }
                            Token::Op {
                                op: Op::RParen,
                                span,
                            } => {
                                depth -= 1;
                                if depth == 0 {
                                    self.bump()?;
                                    break;
                                }
                                if span.start > last_end {
                                    buf.push(b' ');
                                }
                                buf.push(b')');
                                last_end = span.end;
                                self.bump()?;
                            }
                            Token::Eof { .. } => {
                                return Err(
                                    self.err(last_end, "unterminated array assignment".into())
                                );
                            }
                            other => {
                                let span = match other {
                                    Token::Word { span, .. }
                                    | Token::Op { span, .. }
                                    | Token::FdPrefix { span, .. }
                                    | Token::Newline { span, .. }
                                    | Token::HereDocBody { span, .. } => *span,
                                    Token::Eof { pos } => Span {
                                        start: *pos,
                                        end: *pos,
                                    },
                                };
                                if span.start > last_end {
                                    buf.push(b' ');
                                }
                                let t = self.bump()?;
                                push_token_text_raw(&mut buf, &t);
                                last_end = span.end;
                            }
                        }
                    }
                    a.array_body = Some(BString::from(buf));
                }
                assigns.push(a);
                continue;
            }
            // Otherwise, it's a command word.
            let Token::Word { word, .. } = self.bump()? else {
                unreachable!()
            };
            words.push(word);
        }
        Ok(SimpleCmd {
            assigns,
            words,
            redirs,
        })
    }

    fn parse_redirs(&mut self) -> Result<Vec<Redir>, ParseError> {
        let mut redirs = Vec::new();
        loop {
            if let Token::FdPrefix { fd, .. } = self.peek()? {
                let fd = *fd;
                self.bump()?;
                let r = self.parse_redir(Some(fd))?;
                redirs.push(r);
                continue;
            }
            if let Token::Op { op, .. } = self.peek()?
                && is_redir_op(*op)
            {
                let r = self.parse_redir(None)?;
                redirs.push(r);
                continue;
            }
            break;
        }
        Ok(redirs)
    }

    fn parse_redir(&mut self, fd: Option<u32>) -> Result<Redir, ParseError> {
        let Token::Op { op, .. } = self.bump()? else {
            return Err(self.err(0, "expected redirection operator".into()));
        };
        let redir_op = match op {
            Op::Less => RedirOp::In,
            Op::Great => RedirOp::Out,
            Op::DGreat => RedirOp::Append,
            Op::LessGreat => RedirOp::InOut,
            Op::DLess => RedirOp::HereDoc,
            Op::DLessDash => RedirOp::HereDocStrip,
            Op::TLess => RedirOp::HereString,
            Op::LessAmp => RedirOp::DupIn,
            Op::GreatAmp => RedirOp::DupOut,
            Op::AmpGreat => RedirOp::AllOut,
            Op::AmpDGreat => RedirOp::AllAppend,
            Op::GreatPipe => RedirOp::ClobberOut,
            other => {
                return Err(self.err(0, format!("not a redirection operator: {other:?}")));
            }
        };
        // Target word.
        let target = match self.bump()? {
            Token::Word { word, .. } => word,
            other => {
                let pos = token_pos(&other);
                return Err(self.err(
                    pos,
                    format!("expected redirection target, got {}", token_kind(&other)),
                ));
            }
        };
        // For here-docs, register the delimiter so the lexer captures the
        // body after the next newline. The body is attached in a post-
        // parse pass — see `attach_heredoc_bodies`.
        if matches!(redir_op, RedirOp::HereDoc | RedirOp::HereDocStrip) {
            let (delim, quoted) = heredoc_delim_from_word(&target);
            self.lex.register_pending_heredoc(delim, quoted);
        }
        Ok(Redir {
            fd,
            op: redir_op,
            target,
            heredoc_body: None,
        })
    }
}

// --- helpers ---

#[derive(Clone, Copy)]
enum CompoundKind {
    Subshell,
    BraceGroup,
    If,
    While,
    Until,
    For,
    Case,
    Function,
    DoubleBracket,
}

const FOLLOWERS: &[&[u8]] = &[
    b"then", b"else", b"elif", b"fi", b"do", b"done", b"esac", b"}", b"]]",
];

fn is_keyword(word: &Word, table: &[&[u8]]) -> bool {
    let Some(name) = word_to_name(word.clone()) else {
        return false;
    };
    table.iter().any(|kw| name.as_slice() == *kw)
}

fn word_eq(word: &Word, kw: &[u8]) -> bool {
    if word.parts.len() != 1 {
        return false;
    }
    matches!(&word.parts[0], WordPart::Literal(s) if s.as_slice() == kw)
}

fn word_is_simple_name(word: &Word) -> bool {
    if word.parts.len() != 1 {
        return false;
    }
    let WordPart::Literal(s) = &word.parts[0] else {
        return false;
    };
    let bytes = s.as_slice();
    if bytes.is_empty() {
        return false;
    }
    let first = bytes[0];
    if !(first.is_ascii_alphabetic() || first == b'_') {
        return false;
    }
    bytes
        .iter()
        .all(|&b| b.is_ascii_alphanumeric() || b == b'_')
}

fn word_to_name(word: Word) -> Option<BString> {
    if word.parts.len() != 1 {
        return None;
    }
    match word.parts.into_iter().next().unwrap() {
        WordPart::Literal(s) => Some(s),
        _ => None,
    }
}

fn word_as_assignment(word: &Word) -> Option<Assign> {
    // First part must be a literal containing `name=` or `name+=` at the
    // start. Subsequent parts (or remainder of the literal) are the value.
    let WordPart::Literal(first) = word.parts.first()? else {
        return None;
    };
    let s = first.as_slice();
    let mut i = 0;
    if !s
        .first()
        .is_some_and(|c| c.is_ascii_alphabetic() || *c == b'_')
    {
        return None;
    }
    while i < s.len() && (s[i].is_ascii_alphanumeric() || s[i] == b'_') {
        i += 1;
    }
    if i == 0 {
        return None;
    }
    let (append, eq_at) = if s.get(i) == Some(&b'+') && s.get(i + 1) == Some(&b'=') {
        (true, i + 1)
    } else if s.get(i) == Some(&b'=') {
        (false, i)
    } else {
        return None;
    };
    let name = BString::from(&s[..i]);
    let value_in_first = &s[eq_at + 1..];
    let mut value_parts = Vec::new();
    if !value_in_first.is_empty() {
        value_parts.push(WordPart::Literal(BString::from(value_in_first)));
    }
    value_parts.extend(word.parts.iter().skip(1).cloned());
    Some(Assign {
        name,
        append,
        value: Word { parts: value_parts },
        array_body: None,
    })
}

fn is_redir_op(op: Op) -> bool {
    matches!(
        op,
        Op::Less
            | Op::Great
            | Op::DGreat
            | Op::LessGreat
            | Op::DLess
            | Op::DLessDash
            | Op::TLess
            | Op::LessAmp
            | Op::GreatAmp
            | Op::AmpGreat
            | Op::AmpDGreat
            | Op::GreatPipe
    )
}

fn token_pos(t: &Token) -> usize {
    match t {
        Token::Word { span, .. }
        | Token::FdPrefix { span, .. }
        | Token::Op { span, .. }
        | Token::Newline { span, .. }
        | Token::HereDocBody { span, .. } => span.start,
        Token::Eof { pos } => *pos,
    }
}

fn token_kind(t: &Token) -> &'static str {
    match t {
        Token::Word { .. } => "word",
        Token::FdPrefix { .. } => "fd-prefix",
        Token::Op { .. } => "operator",
        Token::Newline { .. } => "newline",
        Token::HereDocBody { .. } => "heredoc-body",
        Token::Eof { .. } => "EOF",
    }
}

/// Render a token's source-bytes-equivalent into `out`. Used when capturing
/// raw header bytes for constructs we don't fully model (e.g. `(( ... ))`,
/// array literals). Best-effort — preserves enough structure for the
/// runtime to reject the script with a meaningful message.
fn push_token_text_raw(out: &mut Vec<u8>, t: &Token) {
    match t {
        Token::Word { word, .. } => {
            for part in &word.parts {
                match part {
                    WordPart::Literal(s) | WordPart::SingleQuoted(s) => out.extend_from_slice(s),
                    WordPart::DollarVar(name) => {
                        out.push(b'$');
                        out.extend_from_slice(name);
                    }
                    WordPart::DollarBrace(body) => {
                        out.extend_from_slice(b"${");
                        out.extend_from_slice(body);
                        out.push(b'}');
                    }
                    WordPart::DollarDoubleParen(body) => {
                        out.extend_from_slice(b"$((");
                        out.extend_from_slice(body);
                        out.extend_from_slice(b"))");
                    }
                    _ => {}
                }
            }
        }
        Token::Op { op, .. } => out.extend_from_slice(op_text(*op)),
        Token::FdPrefix { fd, .. } => {
            out.extend_from_slice(fd.to_string().as_bytes());
        }
        _ => {}
    }
}

fn push_token_text(out: &mut Vec<u8>, t: &Token) {
    if !out.is_empty() {
        out.push(b' ');
    }
    match t {
        Token::Word { word, .. } => {
            for part in &word.parts {
                match part {
                    WordPart::Literal(s) | WordPart::SingleQuoted(s) => out.extend_from_slice(s),
                    WordPart::DollarVar(name) => {
                        out.push(b'$');
                        out.extend_from_slice(name);
                    }
                    _ => {}
                }
            }
        }
        Token::Op { op, .. } => out.extend_from_slice(op_text(*op)),
        _ => {}
    }
}

fn op_text(op: Op) -> &'static [u8] {
    match op {
        Op::Pipe => b"|",
        Op::PipeAmp => b"|&",
        Op::Amp => b"&",
        Op::AndAnd => b"&&",
        Op::OrOr => b"||",
        Op::Semi => b";",
        Op::SemiSemi => b";;",
        Op::SemiAmp => b";&",
        Op::SemiSemiAmp => b";;&",
        Op::LParen => b"(",
        Op::RParen => b")",
        Op::Less => b"<",
        Op::Great => b">",
        Op::DGreat => b">>",
        Op::DLess => b"<<",
        Op::DLessDash => b"<<-",
        Op::TLess => b"<<<",
        Op::LessGreat => b"<>",
        Op::LessAmp => b"<&",
        Op::GreatAmp => b">&",
        Op::AmpGreat => b"&>",
        Op::AmpDGreat => b"&>>",
        Op::GreatPipe => b">|",
    }
}

fn heredoc_delim_from_word(word: &Word) -> (BString, bool) {
    // The delimiter is quoted if any part of the word is single-quoted,
    // double-quoted, or if any literal contains backslash escapes that
    // would have been removed during tokenisation. For Phase 2 we treat
    // the delimiter as quoted iff the word has any non-literal part.
    let mut quoted = false;
    let mut buf = Vec::new();
    for p in &word.parts {
        match p {
            WordPart::Literal(s) => buf.extend_from_slice(s),
            WordPart::SingleQuoted(s) => {
                quoted = true;
                buf.extend_from_slice(s);
            }
            WordPart::DoubleQuoted(parts) => {
                quoted = true;
                for inner in parts {
                    if let WordPart::Literal(s) = inner {
                        buf.extend_from_slice(s);
                    }
                }
            }
            _ => {}
        }
    }
    (BString::from(buf), quoted)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(src: &str) -> Script {
        parse_script(src.as_bytes()).expect("parse")
    }

    #[test]
    fn empty() {
        let s = parse("");
        assert!(s.stmts.is_empty());
    }

    #[test]
    fn simple_command_with_args() {
        let s = parse("echo hello world");
        assert_eq!(s.stmts.len(), 1);
        match &s.stmts[0].command {
            Command::Simple(c) => {
                assert_eq!(c.words.len(), 3);
                assert!(c.assigns.is_empty());
                assert!(c.redirs.is_empty());
            }
            other => panic!("expected Simple, got {other:?}"),
        }
    }

    #[test]
    fn pipeline() {
        let s = parse("a | b | c");
        let cmd = &s.stmts[0].command;
        match cmd {
            Command::Pipeline(p) => assert_eq!(p.cmds.len(), 3),
            _ => panic!(),
        }
    }

    #[test]
    fn and_or() {
        let s = parse("a && b || c");
        match &s.stmts[0].command {
            Command::AndOr(_) => {}
            _ => panic!(),
        }
    }

    #[test]
    fn assignment_then_command() {
        let s = parse("FOO=bar echo $FOO");
        match &s.stmts[0].command {
            Command::Simple(c) => {
                assert_eq!(c.assigns.len(), 1);
                assert_eq!(c.assigns[0].name.as_slice(), b"FOO");
                assert_eq!(c.words.len(), 2);
            }
            _ => panic!(),
        }
    }

    #[test]
    fn redirect_with_fd() {
        let s = parse("echo hi 2>&1");
        match &s.stmts[0].command {
            Command::Simple(c) => {
                assert_eq!(c.redirs.len(), 1);
                assert_eq!(c.redirs[0].fd, Some(2));
                assert_eq!(c.redirs[0].op, RedirOp::DupOut);
            }
            _ => panic!(),
        }
    }

    #[test]
    fn if_clause() {
        let s = parse("if true; then echo y; else echo n; fi");
        match &s.stmts[0].command {
            Command::If(c) => {
                assert_eq!(c.cond.len(), 1);
                assert_eq!(c.then.len(), 1);
                assert!(c.else_branch.is_some());
            }
            _ => panic!(),
        }
    }

    #[test]
    fn while_clause() {
        let s = parse("while read x; do echo $x; done");
        match &s.stmts[0].command {
            Command::While(_) => {}
            _ => panic!(),
        }
    }

    #[test]
    fn for_clause() {
        let s = parse("for x in a b c; do echo $x; done");
        match &s.stmts[0].command {
            Command::For(c) => {
                assert_eq!(c.var.as_slice(), b"x");
                assert_eq!(c.items.as_ref().unwrap().len(), 3);
            }
            _ => panic!(),
        }
    }

    #[test]
    fn case_clause() {
        let s = parse("case $x in a) echo a;; b|c) echo bc;; *) echo def;; esac");
        match &s.stmts[0].command {
            Command::Case(c) => assert_eq!(c.items.len(), 3),
            _ => panic!(),
        }
    }

    #[test]
    fn function_paren_form() {
        let s = parse("greet() { echo hi; }");
        match &s.stmts[0].command {
            Command::Function(f) => assert_eq!(f.name.as_slice(), b"greet"),
            _ => panic!(),
        }
    }

    #[test]
    fn function_keyword_form() {
        let s = parse("function greet { echo hi; }");
        match &s.stmts[0].command {
            Command::Function(f) => assert_eq!(f.name.as_slice(), b"greet"),
            _ => panic!(),
        }
    }

    #[test]
    fn subshell() {
        let s = parse("(echo a; echo b)");
        match &s.stmts[0].command {
            Command::Subshell(stmts) => assert_eq!(stmts.len(), 2),
            _ => panic!(),
        }
    }

    #[test]
    fn brace_group() {
        let s = parse("{ echo a; echo b; }");
        match &s.stmts[0].command {
            Command::BraceGroup(stmts) => assert_eq!(stmts.len(), 2),
            _ => panic!(),
        }
    }

    #[test]
    fn negation() {
        let s = parse("! true");
        assert!(s.stmts[0].negated);
    }

    #[test]
    fn background() {
        let s = parse("sleep 1 &");
        assert!(s.stmts[0].background);
    }

    #[test]
    fn heredoc_quoted_delim() {
        let s = parse("cat <<'EOF'\nbody $no_expand\nEOF\n");
        match &s.stmts[0].command {
            Command::Simple(c) => {
                assert_eq!(c.redirs.len(), 1);
                let body = c.redirs[0].heredoc_body.as_ref().unwrap();
                assert!(body.quoted_delim);
                assert_eq!(body.parts.len(), 1);
                match &body.parts[0] {
                    WordPart::Literal(s) => assert_eq!(s.as_slice(), b"body $no_expand\n"),
                    _ => panic!(),
                }
            }
            _ => panic!(),
        }
    }

    #[test]
    fn heredoc_unquoted_delim() {
        let s = parse("cat <<EOF\nhi $name\nEOF\n");
        match &s.stmts[0].command {
            Command::Simple(c) => {
                let body = c.redirs[0].heredoc_body.as_ref().unwrap();
                assert!(!body.quoted_delim);
                // hi <space> + DollarVar + newline literal
                assert!(
                    body.parts
                        .iter()
                        .any(|p| matches!(p, WordPart::DollarVar(_)))
                );
            }
            _ => panic!(),
        }
    }
}
