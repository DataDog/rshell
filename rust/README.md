# rshell — Rust port (work in progress)

This subdirectory hosts the in-progress Rust port of `rshell`. The Go
implementation in the repository root remains the source of truth until the
port reaches parity. See `DESIGN.md` for the plan and `PROGRESS.md` for the
phase-by-phase status.

## Quick start

```sh
cd rust
cargo build
cargo test
cargo run --bin rshell-rs -- --version
```

The binary is named `rshell-rs` during cohabitation with the Go `rshell`
binary, and will be renamed once the Go implementation is removed.
