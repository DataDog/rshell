# rshell Rust port — progress tracker

This file is the **resumption point** for the Rust port. If a coding session
is interrupted, a new session reads this file (and recent `git log`) to pick
up where the previous one stopped. See `rust/DESIGN.md` for the full plan
and §4a for the per-phase verification protocol.

## How to use this file

1. At session start: read this file top-to-bottom, then `git log --oneline -20`.
2. Work on tasks in the **active phase**. Tick boxes (`[ ]` → `[x]`) as
   tasks land. Add a one-line note under the task if anything non-obvious
   came up.
3. Run the **verification block** at the end of the active phase. Every
   command must pass before flipping the phase to **done**.
4. Verify CI: after pushing, run the **CI verification** snippet at the
   bottom of the phase to confirm GitHub Actions is green.
5. When done: flip the phase to **done**, update **Active phase** to the
   next one, commit + push (per `DESIGN.md` §6.1), and start the next phase
   immediately. **Do not pause for human approval** — the protocol is
   self-validating.
6. Never delete completed entries — they are the breadcrumb trail. Add new
   subtasks as `[ ]` rows if scope grows.
7. If a task is blocked by a real defect (not a documented limitation),
   mark it `[~]` and add a **Blocker:** note.

Statuses: `[ ]` not started · `[~]` in progress / blocked · `[x]` done.

## Snapshot

- **Active phase:** Phase 2 — `rshell-parser`
- **Last updated:** 2026-05-06 (Phase 1 done — 2585 passed / 0 failed / 58 skipped)
- **Branch:** `alex/rust`
- **Binary name during cohabitation:** `rshell-rs`
- **Go removal:** out of scope for this branch; handled separately by the user

### Phase status table

| Phase | Status | Last commit             | CI run             | CI conclusion |
|-------|--------|-------------------------|--------------------|---------------|
| 0     | done   | `6678c83b`              | `25425396174`      | startup_failure (workflow fix landed in `c7ac2339`) |
| 0+CI  | done   | `c7ac2339`              | `25426561894`      | success (Linux + macOS + Windows) |
| 1     | done   | `56d803dc` → `c7ac2339` | `25426561894`      | success |
| 2     | not started |                     |                    |               |
| 3     | not started |                     |                    |               |
| 4     | not started |                     |                    |               |
| 5     | not started |                     |                    |               |
| 6     | not started |                     |                    |               |

After every phase, append a row here with the commit SHA, CI run ID
(`gh run list --branch alex/rust --workflow Rust --limit 1 --json
databaseId --jq '.[0].databaseId'`), and the CI conclusion
(`gh run view <id> --json conclusion --jq '.conclusion'`). The row is
not allowed to claim "done" unless the conclusion is `success`.

## CI verification snippet (used after every push)

```sh
# Wait until the latest run on alex/rust completes, then assert success.
RUN_ID=$(gh run list --branch alex/rust --workflow Rust --limit 1 --json databaseId --jq '.[0].databaseId')
gh run watch "$RUN_ID" --exit-status
gh run view "$RUN_ID" --json conclusion --jq '.conclusion'  # must print "success"
```

If `success` is not printed, the phase is **not** complete — investigate
the failing job, fix, push a new commit, re-run the snippet.

## Phase 0 — scaffolding

**Status:** done
**Exit criterion:** `cargo build`, `cargo fmt --check`, `cargo clippy`,
`cargo test` all green on Linux, macOS, and Windows; CI green.

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
- [x] CI green on the pushed commit (Linux, macOS, Windows).

### Phase 0 verification

```sh
cd rust
cargo fmt --all --check
cargo clippy --all-targets -- -D warnings
cargo build --all-targets
cargo test --all-targets
./target/debug/rshell-rs --version  # must print "rshell-rs 0.1.0 ..."
```

Then run the CI verification snippet above.

## Phase 1 — test runner driving Go `rshell`

**Status:** done
**Exit criterion:** `rshell-test-runner` reports 0 failures across the full
`tests/scenarios/` corpus, with all unmatched scenarios classified as
documented skips. `cargo` checks green; CI green.

- [x] Read the Go test runner (`tests/scenarios_test.go`) and enumerate
      every YAML field used in scenarios.
- [x] Define `Scenario` / `Setup` / `SetupFile` / `Input` / `Expected`
      structs in `rshell-test-runner::scenario` with serde defaults.
- [x] Custom deserializer for `chmod` accepting both ints and `0644`-style
      strings (YAML 1.2 leading-zero numbers parse as strings).
- [x] `rshell-test-runner::setup` — applies `setup.files`, supports content,
      chmod, symlinks, and RFC3339 `mod_time` (with a hand-rolled parser
      so we don't need a date crate).
- [x] `rshell-test-runner::assert` — mirrors `assertExpectations` byte-for-
      byte, including `*_windows`, `stdout_contains`, `stdout_unordered`.
- [x] `rshell-test-runner::runner` — subprocess driver that pipes the
      script via stdin, sets CWD to the test temp dir, applies
      `--allowed-paths` / `--allow-all-commands` / `--allowed-commands`,
      and drains stdout/stderr concurrently to avoid pipe-buffer deadlocks.
- [x] Document the subprocess-mode skip reasons (interpreter_env,
      containerized, symlink-on-Windows, allowed_paths CWD mismatch).
- [x] CLI: `rshell-test-runner [--bin <path>] [--filter <substr>] [--fail-fast] <scenarios-dir>`.
- [x] Verify against the full corpus: 2585 passed / 0 failed / 58 skipped.
- [x] Commit + push as Phase 1.
- [x] CI green on the pushed commit.

### Phase 1 verification

```sh
cd /Users/alexandre.yang/worktrees/rshell/alex/rust   # repo root
go build -o rshell ./cmd/rshell
cd rust
cargo build --release --bin rshell-test-runner
./target/release/rshell-test-runner \
    --bin ../rshell \
    ../tests/scenarios \
    | tail -1
# Expected: "summary: 2585 passed, 0 failed, 58 skipped, 2643 total in <time>"
# Required: failed == 0; passed + skipped == total.
```

The exact number of skips (currently 58 = 7 interpreter_env + 9
containerized + 42 allowed_paths CWD mismatch) may shift slightly as
new scenarios are added; only **failed must be 0** to consider the
phase verified.

CI verification: run the snippet at the top of this file.

## Phase 2 — `rshell-parser`

**Status:** not started
**Exit criterion:** Every script in `tests/scenarios/` round-trips through
the parser without error; `cargo` checks green; CI green.

- [ ] Survey `mvdan.cc/sh/v3/syntax` to enumerate AST node types we need.
- [ ] Define AST types in `rshell-parser::ast` using `bstr::BString` for
      every shell-value field.
- [ ] Tokenizer (handles quoting rules, here-doc opening, brace tracking).
- [ ] Parser: simple commands → pipelines → and/or → lists →
      compound (if/while/until/for/case/subshell/block/function).
- [ ] Parameter expansion / arithmetic expansion / command substitution /
      brace expansion at the *parse* level (preserves AST faithfully;
      evaluation is `rshell-expand`'s job).
- [ ] Redirection parsing (every operator: `>`, `>>`, `<`, `<>`, `<<`,
      `<<-`, `<<<`, `>&`, `<&`, `&>`, `&>>`, `>|`, `|&`).
- [ ] Build a corpus extractor: `rshell-parser-corpus` integration test
      that walks `tests/scenarios/`, pulls every `input.script`, and
      asserts `parse(script).is_ok()`. Test must fail loudly on any parse
      error.
- [ ] Commit + push as Phase 2.
- [ ] CI green on the pushed commit.

### Phase 2 verification

```sh
cd rust
cargo fmt --all --check
cargo clippy --all-targets -- -D warnings
cargo test -p rshell-parser --all-targets
# The corpus integration test (`tests/scenario_corpus.rs`) must pass:
cargo test -p rshell-parser --test scenario_corpus
```

CI verification: run the snippet at the top of this file.

## Phase 3 — `rshell-expand`, `rshell-sandbox`, minimal `rshell-interp`

**Status:** not started
**Exit criterion:** `rshell-test-runner --bin target/release/rshell-rs`
passes a curated smoke set (echo + cat + pwd + true/false + simple
pipelines + simple redirects + simple variable assignment + simple
if/while/for); `cargo` checks green; CI green.

- [ ] `rshell-sandbox`: `AllowedPaths` API on top of `cap-std`. Match
      the semantics of `allowedpaths/sandbox.go`. Document the Windows gap
      vs. Go's `os.Root` in the crate-level rustdoc.
- [ ] `rshell-expand::word`: parameter expansion (full set per
      `DESIGN.md`).
- [ ] `rshell-expand::brace`: brace expansion.
- [ ] `rshell-expand::glob`: pathname expansion via `globset` walking
      through `cap-std`.
- [ ] `rshell-expand::arith`: arithmetic expansion.
- [ ] `rshell-expand::cmdsubst`: hooks back into the interpreter.
- [ ] `rshell-expand::split`: word-splitting per `IFS`.
- [ ] `rshell-interp::env`: variable scopes (global, function-local,
      readonly, exported).
- [ ] `rshell-interp::redir`: redirection setup using OS pipes and the
      sandbox.
- [ ] `rshell-interp::pipeline`: thread-per-stage with OS pipes.
- [ ] `rshell-interp::control`: if / while / until / for / case /
      subshell / `break` / `continue` / `return`.
- [ ] `rshell-interp::handler`: command dispatch — builtins only.
- [ ] Wire `rshell-cli` to call `rshell-interp` and produce a working
      `rshell-rs` binary that runs `-c "echo hello"` end-to-end.
- [ ] Define `rust/tests/smoke-set.txt` listing the scenario paths that
      must pass under `rshell-rs` for Phase 3 to count as done.
- [ ] Commit + push as Phase 3.
- [ ] CI green on the pushed commit.

### Phase 3 verification

```sh
cd rust
cargo build --release
./target/release/rshell-rs -c "echo hello"   # must print "hello"
./target/release/rshell-test-runner \
    --bin ./target/release/rshell-rs \
    --filter-list ../rust/tests/smoke-set.txt \
    ../tests/scenarios \
    | tail -1
# Required: failed == 0 over the smoke set.
```

CI verification: run the snippet at the top of this file.

## Phase 4 — all 29 builtins

**Status:** not started
**Exit criterion:** `rshell-test-runner --bin target/release/rshell-rs`
reports 0 failures over the full scenario corpus (skips OK); `cargo`
checks green; CI green.

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

File readers:

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

System / network (Linux-heavy):

- [ ] `ps`
- [ ] `ip` (route / link / addr)
- [ ] `ss`
- [ ] `ping`

- [ ] Commit + push as Phase 4 (one commit per builtin recommended for
      bisectability).
- [ ] CI green on the pushed commit.

### Phase 4 verification

```sh
cd rust
cargo build --release
./target/release/rshell-test-runner \
    --bin ./target/release/rshell-rs \
    ../tests/scenarios \
    | tail -1
# Required: failed == 0; skipped count must match the documented
# subprocess-mode limitations only.
```

CI verification: run the snippet at the top of this file.

## Phase 5 — `rshell-analysis` 1:1 port

**Status:** not started
**Exit criterion:** `rshell-analysis` CLI runs against the Rust workspace,
verifies the symbol allowlist for every crate, and reports 0 violations;
`cargo` checks green; CI green.

- [ ] Read the Go `analysis/` package; extract the model (allowed
      symbols, structural rules, allowedpaths/builtins/interp segregation).
- [ ] Decide the Rust analogue: parse `Cargo.lock` + crate sources via
      `syn` to enumerate imported symbols per crate.
- [ ] Port `analysis/structural.go` rules.
- [ ] Port the symbol allowlists (allowedpaths / builtins / interp /
      internal).
- [ ] Wire as a binary target: `cargo run -p rshell-analysis` exits 0 on
      a clean workspace and 1 with diagnostics on a violation.
- [ ] Add a CI step in `.github/workflows/rust.yml` that runs the
      analyser and fails the build on violations.
- [ ] Commit + push as Phase 5.
- [ ] CI green on the pushed commit.

### Phase 5 verification

```sh
cd rust
cargo run -p rshell-analysis --release
echo "exit=$?"   # must be 0
```

CI verification: run the snippet at the top of this file.

## Phase 6 — bake-off and `REPORT.html`

**Status:** not started
**Exit criterion:** `rust/REPORT.html` exists, embeds all proof artefacts,
and the CI pipeline on the final Phase 6 commit is green.

- [ ] `rust/scripts/build-report.sh` (deterministic generator).
- [ ] Build both binaries in release with LTO; record sizes.
- [ ] `hyperfine` cold-start comparison (`rshell -c true` vs `rshell-rs -c true`).
- [ ] `/usr/bin/time -v` peak RSS on a representative scenario.
- [ ] Wall-time of full scenario suite via each binary.
- [ ] Capture `rshell-test-runner --bin ./target/release/rshell-rs ../tests/scenarios`
      summary and per-phase verification output.
- [ ] Capture the GitHub Actions run URL + conclusion for the final
      commit using `gh run view`.
- [ ] Generate `rust/REPORT.html` (single-file, embedded CSS).
- [ ] Commit + push as Phase 6.
- [ ] CI green on the pushed commit.

Note: removing the Go implementation is **out of scope** — the user will
handle that separately. Phase 6 is finished when `REPORT.html` is
committed and the CI on that commit is green.

### Phase 6 verification

```sh
cd rust
./scripts/build-report.sh   # must exit 0
test -s REPORT.html         # must be non-empty
# Sanity check that the report contains the key sections:
grep -q "Phase 0" REPORT.html
grep -q "Phase 6" REPORT.html
grep -q "scenario-summary" REPORT.html
```

CI verification: run the snippet at the top of this file.

## Notes / blockers

(Add freeform notes here. One bullet per blocker, dated.)

- 2026-05-06: Phase 1 — the Go binary doesn't expose `--workdir`,
  `--interpreter-env`, or `--host-prefix`, so 58 scenarios are
  structurally unreachable in subprocess mode. They become reachable
  again once Phase 3 lands an in-process Rust runner. Not a blocker.
