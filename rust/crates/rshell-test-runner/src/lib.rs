//! YAML scenario harness for the rshell port.
//!
//! Phase 1 implementation: drives an external `rshell` (or `rshell-rs`)
//! binary as a subprocess. Subprocess mode cannot exercise scenarios that
//! depend on in-process interpreter knobs not exposed by the CLI:
//!
//! - `interpreter_env` (no `--interpreter-env` flag).
//! - `containerized` (no `--host-prefix` flag).
//!
//! Such scenarios are reported as **skipped** with a clear reason rather
//! than failed. Once the Rust interpreter is wired up (Phase 3+) and
//! optionally exposed as a library, an in-process runner can replace the
//! subprocess path.

pub mod assert;
pub mod runner;
pub mod scenario;
pub mod setup;

pub use assert::{AssertError, assert_expectations};
pub use runner::{Outcome, RunOptions, run_scenario};
pub use scenario::{Expected, Input, Scenario, Setup, SetupFile};
