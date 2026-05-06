//! `AllowedPaths` sandbox over `cap-std`.
//!
//! Behaviour parity with `allowedpaths/sandbox.go` is the goal. On Windows,
//! `cap-std` does not currently provide the same atomic guarantees as Go's
//! `os.Root` — this gap will be documented per call site as the port lands.
//!
//! Phase 0: scaffolding only. See `rust/PROGRESS.md`.
