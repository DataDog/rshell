//! AST types for shell scripts. Replaces what `mvdan.cc/sh/v3/syntax`
//! provides on the Go side.
//!
//! Every shell-value field is a `bstr::BString` because shell values are
//! byte streams (filenames in non-UTF-8 locales, binary data through `cat`,
//! etc.) and `String` would corrupt them.

use bstr::BString;

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Script {
    pub stmts: Vec<Stmt>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stmt {
    pub command: Command,
    /// Leading `!` negation.
    pub negated: bool,
    /// Trailing `&` for background execution.
    pub background: bool,
    /// Redirections attached at the *statement* level. Note: simple-command
    /// redirections are parsed into `SimpleCmd::redirs` instead, since they
    /// can be interleaved with assignments and arguments.
    pub redirs: Vec<Redir>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Command {
    Simple(SimpleCmd),
    Pipeline(Pipeline),
    AndOr(AndOr),
    /// `(...)`
    Subshell(Vec<Stmt>),
    /// `{ ...; }`
    BraceGroup(Vec<Stmt>),
    If(IfCmd),
    While(WhileCmd),
    Until(UntilCmd),
    For(ForCmd),
    Case(CaseCmd),
    Function(FunctionDef),
    /// `[[ ... ]]` — body kept as raw bytes for now (Phase 2 only parses
    /// the outer structure; expression evaluation is Phase 3's job).
    DoubleBracket(BString),
    /// `(( ... ))` — body kept as raw bytes for now.
    Arith(BString),
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct SimpleCmd {
    pub assigns: Vec<Assign>,
    pub words: Vec<Word>,
    pub redirs: Vec<Redir>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Assign {
    pub name: BString,
    /// `name+=value` rather than `name=value`.
    pub append: bool,
    pub value: Word,
    /// Set when the value is a `(...)` array literal (e.g. `ARR=(a b c)`).
    /// We store the raw inside-paren bytes; the runtime decides whether
    /// to accept or reject (rshell currently rejects).
    pub array_body: Option<BString>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Pipeline {
    pub cmds: Vec<Stmt>,
    /// `|&` rather than `|`. Bash treats `cmd1 |& cmd2` as
    /// `cmd1 2>&1 | cmd2`.
    pub all: bool,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AndOr {
    pub left: Box<Stmt>,
    pub op: AndOrOp,
    pub right: Box<Stmt>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AndOrOp {
    AndAnd, // &&
    OrOr,   // ||
}

#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Word {
    pub parts: Vec<WordPart>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WordPart {
    /// Unquoted literal text. Backslash escapes are *removed* during
    /// tokenisation; the bytes here are the post-escape contents.
    Literal(BString),
    /// `'...'` — raw bytes, no escapes processed.
    SingleQuoted(BString),
    /// `"..."` — escapes processed for `$`, `` ` ``, `"`, `\`, and
    /// newline-line-continuation. The result is a sequence of inner parts
    /// (literal runs interleaved with expansions).
    DoubleQuoted(Vec<WordPart>),
    /// `$'...'` — ANSI-C quoting (escape sequences resolved at parse time).
    AnsiCQuoted(BString),
    /// `$"..."` — locale-aware translation; we record the raw inner text.
    LocaleQuoted(BString),
    /// `$name` or `${name}` (shorthand). For `${...}` with modifiers, see
    /// `DollarBrace`.
    DollarVar(BString),
    /// `${ ... }` — the body bytes between the braces. Phase 2 keeps these
    /// raw; the parameter-expansion grammar is parsed in `rshell-expand`.
    DollarBrace(BString),
    /// `$( ... )` — inner statements.
    DollarParen(Vec<Stmt>),
    /// `$(( ... ))` — body bytes.
    DollarDoubleParen(BString),
    /// `` ` ... ` `` — inner statements.
    Backtick(Vec<Stmt>),
    /// Process substitution `<( ... )` or `>( ... )`.
    ProcSubst {
        direction: ProcSubstDir,
        body: Vec<Stmt>,
    },
    /// Extended-glob construct `@(...)`, `*(...)`, `+(...)`, `?(...)`,
    /// `!(...)`. We keep the raw body for now.
    ExtGlob { op: ExtGlobOp, body: BString },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProcSubstDir {
    In,  // <(...)
    Out, // >(...)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ExtGlobOp {
    Once, // ?(...)
    Star, // *(...)
    Plus, // +(...)
    At,   // @(...)
    Not,  // !(...)
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Redir {
    /// Explicit fd prefix, e.g. `2>` has `fd = Some(2)`.
    pub fd: Option<u32>,
    pub op: RedirOp,
    pub target: Word,
    /// For here-doc operators (`<<`, `<<-`), the body lines parsed from
    /// the lines following the operator's host line.
    pub heredoc_body: Option<HereDocBody>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HereDocBody {
    /// True if the delimiter was quoted (no expansion of the body).
    pub quoted_delim: bool,
    /// Body parts. For unquoted delimiters this preserves $-expansions as
    /// `WordPart` variants; for quoted delimiters the body is a single
    /// `Literal` part.
    pub parts: Vec<WordPart>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RedirOp {
    /// `<`
    In,
    /// `>`
    Out,
    /// `>>`
    Append,
    /// `<>`
    InOut,
    /// `<<`
    HereDoc,
    /// `<<-` (leading-tab strip)
    HereDocStrip,
    /// `<<<`
    HereString,
    /// `>&`
    DupOut,
    /// `<&`
    DupIn,
    /// `&>`
    AllOut,
    /// `&>>`
    AllAppend,
    /// `>|`
    ClobberOut,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct IfCmd {
    pub cond: Vec<Stmt>,
    pub then: Vec<Stmt>,
    pub elifs: Vec<ElifBranch>,
    pub else_branch: Option<Vec<Stmt>>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ElifBranch {
    pub cond: Vec<Stmt>,
    pub then: Vec<Stmt>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WhileCmd {
    pub cond: Vec<Stmt>,
    pub body: Vec<Stmt>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct UntilCmd {
    pub cond: Vec<Stmt>,
    pub body: Vec<Stmt>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ForCmd {
    pub var: BString,
    /// `None` means `for x do …` — bash defaults to `"$@"` when the
    /// `in <words>` clause is omitted.
    pub items: Option<Vec<Word>>,
    /// Set when the loop is C-style (`for ((init; cond; update))`). The
    /// inner three semicolon-separated expressions are kept as raw bytes;
    /// the runtime decides what to do (rshell currently rejects).
    pub c_style: Option<BString>,
    pub body: Vec<Stmt>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CaseCmd {
    pub word: Word,
    pub items: Vec<CaseItem>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CaseItem {
    /// One or more patterns separated by `|`.
    pub patterns: Vec<Word>,
    pub body: Vec<Stmt>,
    /// Terminator: `;;` (Break), `;&` (Fallthrough), `;;&` (RetestNext).
    pub term: CaseTerm,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CaseTerm {
    Break,       // ;;
    Fallthrough, // ;&
    RetestNext,  // ;;&
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FunctionDef {
    pub name: BString,
    pub body: Box<Stmt>,
}
