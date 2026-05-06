# rshell Rust port — progress tracker

This file is the **resumption point** for the Rust port. If a coding session
is interrupted, a new session reads this file (and recent `git log`) to pick
up where the previous one stopped. See `rust/DESIGN.md` for the full plan.

## How to use this file

1. At session start: read this file top-to-bottom, then `git log --oneline -20`.
2. Work on tasks in the **active phase**. Tick boxes (`[ ]` → `[x]`) as
   tasks land. Add a one-line note under the task if anything non-obvious
   came up.
3. When a phase's exit criterion is met: flip its status to **done**,
   update **Active phase** to the next one, commit + push (per the
   policy in `DESIGN.md` §6.1).
4. Never delete completed entries — they are the breadcrumb trail. Add new
   subtasks as `[ ]` rows if scope grows.
5. If a task is blocked, mark it `[~]` and add a **Blocker:** note.

Statuses: `[ ]` not started · `[~]` in progress / blocked · `[x]` done.

## Snapshot

- **Active phase:** Phase 1 — test runner shelling out to Go `rshell`
- **Last updated:** 2026-05-06 (Phase 0 done)
- **Branch:** `alex/rust`
- **Binary name during cohabitation:** `rshell-rs`

## Phase 0 — scaffolding

**Status:** done
**Exit criterion:** `cargo build`, `cargo fmt --check`, `cargo clippy`,
`cargo test` all green on Linux, macOS, and Windows.

- [x] Write `rust/DESIGN.md`
- [x] Write `rust/PROGRESS.md` (this file)
- [x] Create `rust/Cargo.toml` (virtual workspace)
- [x] Create `rust/rust-toolchain.toml` (stable)
- [x] Create `rust/.gitignore`
- [x] Create empty crate skeletons under `rust/crates/`:
  - [x] `rshell-cli` (binary, prints `--version` and `--help`)
  - [x] `rshell-interp`
  - [x] `rshell-parser`
  - [x] `rshell-expand`
  - [x] `rshell-builtins`
  - [x] `rshell-sandbox`
  - [x] `rshell-analysis`
  - [x] `rshell-test-runner`
- [x] Wire `rshell-cli` to print a placeholder `--version` so we can verify
      the binary builds and runs.
- [x] Extend top-level `Makefile` with `rust-build`, `rust-test`,
      `rust-fmt`, `rust-fmt-check`, `rust-lint`, `rust-all` targets.
- [x] Add `.github/workflows/rust.yml` for the Linux + macOS + Windows ×
      stable matrix.
- [x] Confirm `cargo fmt --all --check`, `cargo clippy -- -D warnings`,
      `cargo build`, `cargo test` all green locally on macOS.
- [x] Commit + push as Phase 0.

## Phase 1 — test runner shelling out to Go `rshell`

**Status:** not started
**Exit criterion:** Full YAML scenario suite runs from the Rust runner
against the existing Go binary with output parity to the Go runner.

- [ ] Read the Go test runner under `tests/` to enumerate every YAML
      assertion field used in scenarios.
- [ ] Define `Scenario` / `Expect` structs in `rshell-test-runner` with
      `serde(deny_unknown_fields)` over the full schema (`stdout`,
      `stdout_contains`, `stdout_windows`, `stderr*`, `exit_code`,
      `input.script`, `skip_assert_against_bash`, `allowed_paths`, …).
- [ ] Implement subprocess execution: spawn `rshell` (path configurable),
      pipe `input.script` to stdin, capture stdout/stderr/exit code with a
      timeout matching the executor (30s).
- [ ] Implement assertion comparison matching the Go runner's semantics
      byte-for-byte (including Windows overrides).
- [ ] Implement bash comparison mode equivalent to `RSHELL_BASH_TEST=1`,
      using Docker `debian:bookworm-slim`.
- [ ] CLI: `rshell-test-runner [--bin <path>] [--bash] [--filter <glob>] <scenarios-dir>`.
- [ ] Run against `tests/scenarios/` with `--bin $(which rshell)` and
      verify pass-count matches the Go runner.
- [ ] Commit + push as Phase 1.

## Phase 2 — `rshell-parser`

**Status:** not started
**Exit criterion:** Every script in `tests/scenarios/` round-trips through
the parser without error and produces an AST.

- [ ] Survey `mvdan.cc/sh/v3/syntax` to enumerate AST node types we need
      (most are already enumerated in `analysis/symbols_interp.go`).
- [ ] Define AST types in `rshell-parser::ast` using `bstr::BString` for
      every shell-value field.
- [ ] Tokenizer (handles quoting rules, here-doc opening, brace tracking).
- [ ] Parser: simple commands → pipelines → and/or → lists →
      compound (if/while/until/for/case/subshell/block/function).
- [ ] Parameter expansion / arithmetic expansion / command substitution /
      brace expansion at the *parse* level (preserves AST faithfully;
      evaluation is `rshell-expand`'s job).
- [ ] Redirection parsing (every operator from
      `mvdan/sh/v3/syntax`'s redirect operator constants — `>`, `>>`,
      `<`, `<>`, `<<`, `<<-`, `<<<`, `>&`, `<&`, `&>`, `&>>`, `>|`, `|&`).
- [ ] Round-trip test: extract every `input.script` from `tests/scenarios/`
      into a corpus, parse all of them, fail on any parse error.
- [ ] Commit + push as Phase 2.

## Phase 3 — `rshell-expand`, `rshell-sandbox`, minimal `rshell-interp`

**Status:** not started
**Exit criterion:** A representative subset of scenarios (echo, cat, pwd,
true/false, simple pipelines, simple redirects, simple variable assignment,
simple if/while/for) passes against `rshell-rs`.

- [ ] `rshell-sandbox`: `AllowedPaths` API on top of `cap-std`. Match the
      semantics of `allowedpaths/sandbox.go`. Document the Windows gap
      vs. Go's `os.Root` in the crate-level rustdoc.
- [ ] `rshell-expand::word`: parameter expansion (`$x`, `${x}`, `${x:-y}`,
      `${x:=y}`, `${x:?y}`, `${x:+y}`, `${#x}`, `${x#p}`, `${x##p}`,
      `${x%p}`, `${x%%p}`, `${x/p/r}`, `${x//p/r}`, `${x:offset:length}`,
      indirect `${!x}`, length `${#@}`, …).
- [ ] `rshell-expand::brace`: brace expansion (`{a,b,c}`, `{1..10}`,
      `{1..10..2}`, `{a..z}`).
- [ ] `rshell-expand::glob`: pathname expansion via `globset` walking
      through `cap-std`. Respect `nullglob`, `failglob`, `nocaseglob`,
      `dotglob`, `globstar` if/where bash does.
- [ ] `rshell-expand::arith`: arithmetic expansion `$(( ... ))`.
- [ ] `rshell-expand::cmdsubst`: hooks back into the interpreter for
      `$( ... )` and backticks.
- [ ] `rshell-expand::split`: word-splitting per `IFS`.
- [ ] `rshell-interp::env`: variable scopes (global, function-local,
      readonly, exported), `$?`, `$#`, `$@`, `$*`, `$0`–`$9`, `$$`, `$!`.
- [ ] `rshell-interp::redir`: redirection setup using OS pipes and the
      sandbox `Dir` for opening files.
- [ ] `rshell-interp::pipeline`: thread-per-stage with OS pipes; correct
      exit-code propagation (last stage by default; `pipefail` option).
- [ ] `rshell-interp::control`: if / while / until / for / case /
      subshell / `break` / `continue` / `return`.
- [ ] `rshell-interp::handler`: command dispatch — for now, builtins only
      (no PATH lookup). Unknown commands → "command not found", exit 127.
- [ ] Wire `rshell-cli` to call `rshell-interp` and produce a working
      `rshell-rs` binary that runs `-c "echo hello"` end-to-end.
- [ ] Commit + push as Phase 3.

## Phase 4 — all 29 builtins

**Status:** not started
**Exit criterion:** 100% scenario parity for non-network scenarios; binary
matches Go output byte-for-byte (and bash, for `skip_assert_against_bash:
false` scenarios).

Builtins ordered by dependency / difficulty. Each is one task; mark as
done only when its scenarios pass under `rshell-rs`.

Trivial / control:

- [ ] `true`
- [ ] `false`
- [ ] `:` (no-op, may be parser-level)
- [ ] `exit`
- [ ] `echo`
- [ ] `pwd`
- [ ] `printf`
- [ ] `help`
- [ ] `testcmd` (`[ ]` / `test`)
- [ ] `uname`

File readers (need sandbox + line buffers):

- [ ] `cat`
- [ ] `head`
- [ ] `tail`
- [ ] `wc`
- [ ] `cut`
- [ ] `tr`
- [ ] `uniq`
- [ ] `sort`
- [ ] `grep`
- [ ] `sed`
- [ ] `strings_cmd`

Filesystem walkers:

- [ ] `ls`
- [ ] `find`
- [ ] `du`

System / network (Linux-heavy, platform-specific):

- [ ] `ps`
- [ ] `uname` (already listed; cross-platform variants)
- [ ] `ip` (route / link / addr — reads `/proc/net/route` directly)
- [ ] `ss` (reads `/proc/net/{tcp,udp,...}` directly)
- [ ] `ping` (decide on `surge-ping` vs alternative; `cap_net_raw` story)

- [ ] Run the full YAML scenario suite via `rshell-test-runner --bin
      target/release/rshell-rs` and confirm parity.
- [ ] Commit + push as Phase 4 (one commit per builtin recommended for
      bisectability).

## Phase 5 — `rshell-analysis` 1:1 port

**Status:** not started
**Exit criterion:** Equivalent symbol-allowlist verification for the Rust
crate dependency graph; CI fails on disallowed symbols.

- [ ] Read the Go `analysis/` package to extract the model (allowed
      symbols, structural rules, allowedpaths/builtins/interp segregation).
- [ ] Decide the Rust analogue: parse `Cargo.lock` + crate sources via
      `syn` to enumerate imported symbols per crate.
- [ ] Port `analysis/structural.go` rules.
- [ ] Port the symbol allowlists (allowedpaths / builtins / interp /
      internal).
- [ ] Wire as a `cargo xtask analysis` (or standalone binary) that CI runs.
- [ ] Commit + push as Phase 5.

## Phase 6 — bake-off and Go removal decision

**Status:** not started
**Exit criterion:** Numbers documented, decision made on Go removal.

- [ ] Build both binaries in release with LTO; record sizes.
- [ ] `hyperfine` cold-start comparison (`rshell -c true` vs `rshell-rs -c true`).
- [ ] `/usr/bin/time -v` peak RSS on `tests/scenarios/` representative case.
- [ ] Wall-time of full scenario suite via each binary.
- [ ] Document numbers in `rust/README.md`.
- [ ] Decide with user: keep cohabitation, rename `rshell-rs` → `rshell`
      and delete Go, or extend cohabitation.
- [ ] Commit + push as Phase 6.

## Notes / blockers

(Add freeform notes here. One bullet per blocker, dated.)

- _none yet_
