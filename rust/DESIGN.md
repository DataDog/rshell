# rshell Rust port — design doc

Status: **draft / exploratory**. This document captures the plan for a 1:1 port
of `rshell` from Go to Rust, living in the `rust/` subdirectory of this branch
(`alex/rust`) alongside the existing Go implementation until parity is reached.

## 1. Goals and non-goals

### Goals

- Match the runtime behaviour of the Go implementation, as defined by the
  ~2,600 YAML scenarios in `tests/scenarios/` and verified byte-for-byte
  against GNU bash via `RSHELL_BASH_TEST=1`.
- Reduce binary size and peak RSS, and improve startup time. No specific
  numeric targets; best effort, measured at the bake-off stage.
- Preserve the security posture: sandboxed file access, no real binary
  execution, bounded resource use, deterministic output.
- Keep the YAML scenario format unchanged; rewrite only the test runner.

### Non-goals

- Telemetry. The Go implementation has an optional `with_telemetry` build tag
  that wires `pkg/fleet/installer/telemetry`. The Rust port drops this feature
  entirely; there is no `rshell-telemetry` crate. If telemetry is needed
  later, it can be added via the `tracing` ecosystem.
- Real external binary execution. Every command resolves to a builtin. PATH
  lookup and `fork`/`exec` of arbitrary binaries are out of scope. This may
  change in the future but is not part of this port.
- Bug-for-bug compatibility with `mvdan.cc/sh/v3`. Where the YAML harness +
  bash say one thing and `mvdan/sh` says another, follow bash.
- Publishing crates to crates.io. Internal use only for now.

## 2. Workspace layout

```
rust/
├── Cargo.toml                  # virtual workspace manifest
├── rust-toolchain.toml         # pinned stable Rust (1.85+, edition 2024)
├── DESIGN.md                   # this file
├── README.md
└── crates/
    ├── rshell-cli/             # binary crate; produces `rshell-rs`
    ├── rshell-interp/          # runner, redirections, var scope, control flow
    ├── rshell-parser/          # syntax → AST (replaces mvdan/sh/v3/syntax)
    ├── rshell-expand/          # word expansion, globbing, splitting (replaces mvdan/sh/v3/expand)
    ├── rshell-builtins/        # one module per builtin
    ├── rshell-sandbox/         # AllowedPaths over cap-std
    ├── rshell-analysis/        # 1:1 port of analysis/ symbol-allowlist verifier
    └── rshell-test-runner/     # YAML scenario harness + bash comparison
```

The split mirrors the current Go package boundaries so individual crates can
be implemented and tested piecewise. `rshell-parser` and `rshell-expand` are
deliberately separate so they can be reused outside the interpreter (the Go
analyzer also depends on the parser).

The binary is named `rshell-rs` during the cohabitation period to avoid
`$PATH` collisions with the Go `rshell` binary. Once the Go implementation is
removed, the binary will be renamed to `rshell`.

## 3. Foundational design decisions

### 3.1 String model: bytes, not UTF-8

Shell variables, file paths, command output, and process arguments are byte
streams in bash. Rust's `String` is UTF-8 only, which is wrong for shell
semantics: a shell value can legitimately contain invalid UTF-8 (e.g. a
filename in a non-UTF-8 locale, binary data piped through `cat`).

**Choice:** use `bstr::BString` / `bstr::BStr` for shell values throughout the
crates. Convert to `&str` only at output boundaries when the value is known
to be UTF-8 (e.g. error messages we construct ourselves). Path arguments to
syscalls go through `OsStr` / `OsString` / `Path` / `PathBuf`.

This is the single biggest correctness decision and it touches every crate.
Getting it right early is much cheaper than retrofitting.

### 3.2 Concurrency: synchronous + threads, no async runtime

Pipelines, here-docs, and command substitution need concurrent producers and
consumers. We use `std::thread` and OS pipes (`std::io::pipe` from std on
recent stable, or the `os_pipe` crate as a fallback). Rationale:

- Smaller binary (no tokio runtime).
- Faster cold startup (no runtime init).
- Fewer dependencies.
- Pipelines are bounded fan-out (typically 2–4 stages); thread cost is
  negligible compared to typical command runtime.

If a specific feature later needs async (e.g. a network builtin), it can be
isolated behind a thread boundary rather than coloring the whole stack.

### 3.3 CLI parsing: `clap` (derive)

`cobra` → `clap` derive is a clean port for the top-level CLI. For the
per-builtin flag parsing currently done with `pflag`, we use the `clap`
builder API per builtin — repetitive but matches the project rule that all
flag parsing must be `pflag`-equivalent (POSIX/GNU conventions, `--` end of
flags, combined short bool flags, glued-value short flags).

### 3.4 Regex and globbing

- Regex: `regex` crate (RE2-derived, linear time). Matches the project rule
  preferring linear-time regex engines over backtracking ones, so existing
  ReDoS protections are inherited for free.
- Globs: `globset` for compiled multi-pattern matching, hand-rolled per-path
  walking using `cap-std` (so glob expansion respects the sandbox).

### 3.5 Logging

`tracing` for diagnostic logs. No telemetry crate.

### 3.6 Error model

- `thiserror` for typed error enums at every public crate boundary.
- `anyhow` only inside the binary crate (`rshell-cli`) for top-level
  composition.

Builtins return a typed `BuiltinError` that the interpreter maps to an exit
code and a stderr message — never propagated as `?` past the interpreter
boundary.

### 3.7 Sandbox

`cap-std` provides `Dir` capability handles backed by `openat` on Unix.
Behaviour parity with Go's `os.Root` on Linux and macOS. Document the gap on
Windows (cap-std does not currently provide the same atomic guarantees there
that Go's `os.Root` does on Linux). The gap is informational only — both
implementations have weaker Windows guarantees than they have on Linux.

### 3.8 MSRV and edition

`rust-toolchain.toml` pins stable, edition 2024, MSRV 1.85.0. CI matrix tests
the pinned toolchain on Linux, macOS, and Windows.

### 3.9 Dependency budget (initial)

Core crates expected, with rationale:

| Crate          | Purpose                                                | Justification                                          |
|----------------|--------------------------------------------------------|--------------------------------------------------------|
| `bstr`         | Byte-string types                                      | UTF-8 disagreement with shell semantics (§3.1)         |
| `clap`         | CLI parsing                                            | cobra/pflag analogue (§3.3)                            |
| `cap-std`      | Capability-based filesystem                            | Sandbox (§3.7)                                         |
| `regex`        | Regex                                                  | RE2; linear time (§3.4)                                |
| `globset`      | Glob compilation                                       | (§3.4)                                                 |
| `thiserror`    | Error derive                                           | Error model (§3.6)                                     |
| `anyhow`       | Error composition in binary                            | Error model (§3.6)                                     |
| `tracing`      | Diagnostic logs                                        | (§3.5)                                                 |
| `serde`, `serde_yaml` | YAML scenario loading                          | Test runner only                                       |
| `os_pipe`      | Cross-platform pipes (if std::io::pipe insufficient)   | Pipelines                                              |
| `nix`          | Unix syscalls (Unix-only crates)                       | Sandbox internals, signal handling                     |
| `windows-sys`  | Windows syscalls (Windows-only crates)                 | Sandbox internals                                      |

No async runtime. No HTTP client (telemetry dropped). No serialization
beyond YAML for tests.

## 4. Builtin inventory

29 builtins to port (excluding `break` and `continue`, which are interpreter
control-flow constructs, not external commands):

```
cat, cut, du, echo, exit, false, find, grep, head, help, ip, ls, ping,
printf, ps, pwd, sed, sort, ss, strings_cmd, tail, testcmd, tr, true,
uname, uniq, wc
```

Plus interpreter-internal: `break`, `continue`, `exit`, plus `set`, `export`,
`unset`, `local`, `readonly`, `declare`, `:` (handled in interp, not as
builtin commands).

Notable per-builtin notes:

- **`ip route` / `ss`**: read `/proc/net/{route,tcp,udp,...}` directly,
  bypassing `AllowedPaths`. This is documented in `CLAUDE.md` as a security
  design decision — the paths are non-user-controllable kernel pseudo-files.
  Carry the same property forward in Rust; the Linux-only paths are
  hardcoded.
- **`ping`**: uses `prometheus-community/pro-bing` in Go. Rust analogue is
  `surge-ping` (raw ICMP) or shelling-out — the latter is forbidden by §1.
  Confirm the ICMP capability story (raw socket on Linux requires
  `cap_net_raw` or unprivileged ping via `IPPROTO_ICMP` socket on Linux).
- **`find`**: must walk via the cap-std `Dir` handle to keep sandbox
  enforcement.
- **`grep` / `sed`**: use `regex` only. Document any GNU-specific extensions
  the YAML harness exercises (Perl-like backreferences are unsupported by
  RE2; bash itself uses ERE/BRE so this is unlikely to bite).

## 4a. Per-phase verification protocol

Every phase ends with a self-contained verification step that proves the
phase is complete *without human judgement*. The verification commands are
copy-paste runnable, and their output is captured into the final
`rust/REPORT.html` (see §6.2). A phase is not "done" until:

1. Its `cargo` checks (`cargo fmt --all --check`, `cargo clippy --all-targets
   -- -D warnings`, `cargo build --all-targets`, `cargo test --all-targets`)
   pass locally on the host platform.
2. The phase-specific verification command(s) listed in `PROGRESS.md` pass.
3. **GitHub Actions CI on the pushed commit reports green** for every job
   in `.github/workflows/rust.yml` (Linux + macOS + Windows). This is
   verified with `gh run list --branch alex/rust --limit 1` followed by
   `gh run view <id>` until the conclusion is `success`.
4. The phase's row in `PROGRESS.md` is ticked off and the commit message
   includes a one-line summary of the verification result.

The protocol is explicit so that an interrupted session can re-run the
verification deterministically. No phase requires human approval to
proceed; the next phase begins as soon as the previous phase's
verification has passed.

## 5. Test strategy

### 5.1 Scenario harness — port first, before any runtime work

`rshell-test-runner` is implemented in Phase 1, **before** the parser or
interp. Initially it shells out to the existing Go `rshell` binary. This
gives us:

- Confidence the harness works in Rust.
- An oracle for every subsequent Rust crate.
- A way to bisect regressions: at any phase, we can flip individual scenarios
  to run against `rshell-rs` instead of `rshell` and see what breaks.

Implementation: parse the YAML, fork `rshell` (or `rshell-rs`) as a
subprocess, capture stdout/stderr/exit code, compare to expectations using
the same `stdout` / `stderr` / `*_contains` / `*_windows` semantics. Reuse
the existing Docker-based bash comparison (`RSHELL_BASH_TEST=1` equivalent).

### 5.2 Unit tests

Each crate has its own `#[cfg(test)]` module. The parser crate tests against
a corpus extracted from the scenario YAML (round-trip every script).

### 5.3 Cross-platform CI

GitHub Actions matrix: Linux + macOS + Windows × stable Rust. The bash
comparison test is Linux-only (Docker-based) and matches the existing Go
arrangement.

### 5.4 Bake-off

Once parity is reached, run the full scenario suite against both binaries
and compare:

- Binary size (`ls -l`).
- Cold startup (`hyperfine 'rshell -c "true"'` vs `rshell-rs`).
- Peak RSS on a representative scenario (`/usr/bin/time -v`).
- Wall time on the full scenario suite.

These numbers go into the README and gate the eventual switch.

## 6. Phased migration

| Phase | Deliverable                                                          | Exit criterion                                                                |
|-------|----------------------------------------------------------------------|-------------------------------------------------------------------------------|
| 0     | Workspace scaffolding, CI, toolchain, Makefile targets               | `make rust-all` green; CI green on all 3 OSes                                 |
| 1     | `rshell-test-runner` driving Go `rshell`                             | `rshell-test-runner` reports 0 failures over all scenarios; skips documented  |
| 2     | `rshell-parser`                                                      | Round-trips every script in `tests/scenarios/` (test in `rshell-parser`)      |
| 3     | `rshell-expand`, `rshell-sandbox`, minimal `rshell-interp`           | `rshell-test-runner --bin rshell-rs` passes a curated smoke set               |
| 4     | All 29 builtins ported                                               | `rshell-test-runner --bin rshell-rs` reports 0 failures over non-skip scenarios |
| 5     | `rshell-analysis` 1:1 port                                           | `cargo run -p rshell-analysis` validates the workspace allowlist with 0 errors |
| 6     | Bake-off + `REPORT.html`                                             | `REPORT.html` generated; perf numbers recorded; Go removal is out of scope    |

Each phase ends with a (draft) PR. Phases are sequential because each
depends on the previous; within a phase, builtins in Phase 4 can be done in
parallel. Go removal is **explicitly out of scope** for this branch — it
will be handled later by the user.

### 6.1 Commit and push policy

Every phase ends with a **new commit pushed to `alex/rust`**. No squashing
across phases — each phase is one or more commits, never amended into a
previous phase's commits, and pushed as soon as the phase's exit criterion
is met. This gives a reviewable, bisectable history of the port and lets the
draft PR show concrete progress.

### 6.2 Final report (Phase 6 deliverable)

Phase 6 produces `rust/REPORT.html`, a self-contained HTML page that
proves the port is complete. It includes:

- A summary of every phase with status, commit SHA, and CI conclusion.
- Verification command output (stdout + exit code) for the per-phase
  protocol in §4a.
- Full scenario-suite results from `rshell-test-runner --bin rshell-rs`
  (passed / failed / skipped counts and any divergences from the Go
  baseline).
- Bake-off numbers: binary size (`ls -l`), startup
  (`hyperfine 'rshell-rs -c true'`), peak RSS (`/usr/bin/time -v` on a
  representative scenario), and full-suite wall time vs. the Go binary.
- The GitHub Actions run URL and conclusion for the final commit on
  `alex/rust`, fetched via `gh`.

The report is generated by a script committed under
`rust/scripts/build-report.sh` (or equivalent) so it can be re-run
deterministically. The HTML is single-file and embeds its own CSS — no
external dependencies.

## 7. Risks and unknowns

- **Expansion semantics depth.** `mvdan/sh/v3/expand` has years of bash
  edge-case fixes baked in. Re-deriving them from the scenario corpus alone
  may surface gaps. Mitigation: the bash comparison harness will catch
  divergences early, but expect Phase 3 to be the largest in calendar time.
- **Windows parity.** Several builtins (`ip`, `ss`, `ping`) and the sandbox
  behave differently on Windows in Go. Re-establishing the same parity in
  Rust will require platform-specific code paths and may surface new gaps
  that the Go version papered over.
- **`cap-std` Windows weakness.** Documented and accepted; matches the
  current state of the world for `os.Root` on Windows.
- **`pro-bing` replacement.** No exact Rust equivalent. `surge-ping` may be
  close enough but the YAML scenarios may encode `pro-bing`-specific output
  formatting.
- **Analysis crate port.** 13k LOC of Go-specific symbol-allowlist logic.
  Mechanically translatable but not a small effort. Phase 5 because it does
  not block runtime parity.
- **Effort.** The full port is multi-month. This branch will carry both
  implementations for the duration; Go remains the source of truth until
  Phase 6 closes.

## 8. Out of scope for this doc

- Specific module structure inside each crate.
- Specific function signatures.
- CI workflow YAML.
- Whether to publish any of these crates externally.

These are decided per-phase as the work lands.
