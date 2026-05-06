//! Shell tokenizer and parser. Replaces `mvdan.cc/sh/v3/syntax`.
//!
//! Phase 2 scope: see `rust/PROGRESS.md`. The parser supports the core
//! grammar (simple commands, pipelines, and-or lists, redirections incl.
//! here-docs, if/while/until/for/case/function/brace-group/subshell) and
//! preserves enough word-part structure for Phase 3 expansion.

pub mod ast;
pub mod lex;
pub mod parse;

pub use ast::*;
pub use lex::{LexError, Lexer, Op, Span, Token};
pub use parse::{ParseError, parse_inner_script, parse_script};
