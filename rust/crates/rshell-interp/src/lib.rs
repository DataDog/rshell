//! Shell interpreter: runner, redirections, variable scopes, control flow.
//!
//! Phase 3 baseline. See `rust/PROGRESS.md`.

pub mod builtin;
pub mod env;
pub mod expand;
pub mod runner;

pub use builtin::{Builtin, BuiltinRegistry, CallCtx};
pub use env::{Env, Variable};
pub use expand::{expand_to_fields, expand_to_string};
pub use runner::{RunError, Runner, run_script, run_stmt};
