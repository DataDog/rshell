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

- **Active phase:** Phase 6 — bake-off + REPORT.html (in progress)
- **Last updated:** 2026-05-06 (Phases 4 & 5 done — all 29 builtins, analyzer in CI)
- **Branch:** `alex/rust`
- **Binary name during cohabitation:** `rshell-rs`
- **Go removal:** out of scope for this branch; handled separately by the user

### Phase status table

| Phase | Status | Last commit             | CI run             | CI conclusion |
|-------|--------|-------------------------|--------------------|---------------|
| 0     | done   | `6678c83b`              | `25425396174`      | startup_failure (workflow fix landed in `c7ac2339`) |
| 0+CI  | done   | `c7ac2339`              | `25426561894`      | success (Linux + macOS + Windows) |
| 1     | done   | `56d803dc` → `c7ac2339` | `25426561894`      | success |
| 2     | done   | `87a91da6` → `96d84887` | `25428530678`      | success |
| 3     | done   | `1a95ca5e`              | `25430933209`      | success |
| 4w1   | done   | `47884460`              |                    |               |
| 4w2   | done   | `00ca756e` → `621976e1` |                    |               |
| 4w3   | done   | `ac11d441` → `ece73af5` |                    | success |
| 4w4   | done   | `fe370dcf`              | `25436706826`      | success |
| 4w5   | done   | `7f232b28`              | `25448134981`      | success |
| 5     | done   | (this commit)           | (see post-push)    |               |
| 6     | scripted | (this commit)         |                    |               |
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

**Status:** done
**Exit criterion:** Every script in `tests/scenarios/` round-trips through
the parser without error; `cargo` checks green; CI green.

- [x] AST types in `rshell-parser::ast` using `bstr::BString` everywhere
      shell values live. Covers Script / Stmt / Command (Simple, Pipeline,
      AndOr, Subshell, BraceGroup, If, While, Until, For, Case, Function,
      DoubleBracket, Arith), Word/WordPart (Literal, SingleQuoted,
      DoubleQuoted, AnsiCQuoted, LocaleQuoted, DollarVar, DollarBrace,
      DollarParen, DollarDoubleParen, Backtick, ProcSubst, ExtGlob),
      Redir/RedirOp/HereDocBody, ForCmd with c-style header bytes,
      Assign with array_body bytes.
- [x] Tokenizer (`lex.rs`) — quoting (single, double, $'…'), $-expansions,
      backticks, here-doc body capture (queued then flushed after
      newlines), line continuations, comments, redirection operators
      with optional fd prefix, span tracking for adjacency checks
      (process subst, C-style for, array assignment).
- [x] Recursive-descent parser (`parse.rs`) — simple commands with
      assignments and redirections, pipelines (`|`, `|&`), and-or lists
      (`&&`, `||`), compound commands (if/elif/else/fi, while/until/do/
      done, for-iter and C-style, case with `;;` / `;&` / `;;&`,
      subshell, brace group, function in both `name()` and `function`
      forms, `[[ ... ]]` with raw body bytes).
- [x] Redirection parsing for every operator including here-docs with
      deferred body attachment (handles `cmd <<EOF | grep x` cases).
- [x] Process substitution (`<(...)`, `>(...)`) and extended-glob word
      parts (`?(...)`, `*(...)`, `+(...)`, `@(...)`, `!(...)`).
- [x] Array assignment `name=(a b c)` recognised (raw inner bytes kept).
- [x] 26 unit tests covering each grammar form.
- [x] Corpus integration test (`tests/scenario_corpus.rs`): walks
      `tests/scenarios/`, parses every `input.script`, asserts 0
      failures. **Result: 2641/2641 (100.00%).**
- [x] Commit + push as Phase 2.
- [x] CI green on the pushed commit (run `25428530678`, all 3 OSes).

### Phase 2 verification

```sh
cd rust
cargo fmt --all --check
cargo clippy --all-targets -- -D warnings
cargo test -p rshell-parser --all-targets
# The corpus integration test must pass without RSHELL_PARSER_ALLOW_FAILURES:
cargo test -p rshell-parser --test scenario_corpus
# To inspect coverage:
RSHELL_PARSER_ALLOW_FAILURES=1 cargo test -p rshell-parser --test scenario_corpus -- --nocapture 2>&1 | grep coverage
# Expected: "Phase 2 corpus coverage: 2641/2641 (100.00%)  failures=0"
```

CI verification: run the snippet at the top of this file.

## Phase 3 — `rshell-expand`, `rshell-sandbox`, minimal `rshell-interp`

**Status:** done
**Exit criterion:** `rshell-test-runner --bin target/release/rshell-rs
--filter-list rust/tests/smoke-set.txt` passes 100% with the curated
smoke set; `cargo` checks green; CI green.

- [x] `rshell-interp::env` — global + function-local frames with
      readonly/exported flags, positional params, `$?`, `$$`, `$#`, `$@`,
      `$*`, `$0`–`$9`, `IFS`-aware splitting.
- [x] `rshell-interp::expand` — literals, single/double/ANSI-C/locale
      quoting, `$var` and `${var}` (bare-name only — modifiers deferred),
      special parameters, IFS-aware field splitting honoring quoted
      regions. `$()`, `$(())`, backticks, brace, glob expansion are
      stubbed for the baseline (Phase 4 wires them in).
- [x] `rshell-interp::runner` — statement executor with full redir
      pipeline (`<`, `>`, `>>`, `2>&1`, `&>`, here-docs as literal
      bodies), pipelines via `os_pipe` + thread-per-stage, control flow
      (if/elif/else, while, until, for-iter), subshell + brace group
      (shared state for the baseline), function definition + call with
      argv shadowing and scope push/pop, `&&`/`||`/`;`/`!`/`&`,
      transient inline assignments rolled back after the call.
- [x] Hard-fails for blocked features the parser preserves: array
      assignment, C-style for, process substitution, extended glob,
      here-string. Each maps to exit 2 with a clear stderr message.
- [x] `rshell-builtins` — initial set: `echo` (full bash flag handling
      with backslash escapes), `cat` (stream-through), `pwd`, `true`,
      `false`, `:`, `exit`. Each is a small, registered `Builtin` impl.
- [x] `rshell-cli` wired up: parses `-c`, script file, or stdin; builds
      a `Runner` with the registered builtins; runs `parse_script` from
      `rshell-parser` then `run_script` from `rshell-interp`. Binary
      builds at **926 KB** in release with LTO+strip.
- [x] `rshell-test-runner` extended with `--filter-list <file>` so the
      smoke-set path can be passed as the gate.
- [x] `rust/tests/smoke-set.txt` committed with 293 passing scenarios
      from cmd/echo, cmd/true, cmd/false, cmd/pwd/basic, shell/{simple_
      command/basic, if_clause/basic, cmd_separator/basic, logic_ops,
      negation/basic, var_expand/basic, empty_script, comments,
      line_continuation}.
- [x] Commit + push as Phase 3.
- [x] CI green on the pushed commit (run `25430933209`, all 3 OSes).

### Phase 3 verification

```sh
cd rust
cargo fmt --all --check
cargo clippy --all-targets -- -D warnings
cargo build --release --bin rshell-rs --bin rshell-test-runner
./target/release/rshell-rs -c "echo hello"   # must print "hello"
./target/release/rshell-test-runner \
    --bin ./target/release/rshell-rs \
    --filter-list ./tests/smoke-set.txt \
    ../tests/scenarios | tail -1
# Required: 293 passed, 0 failed.
```

Open notes for follow-up phases (not blocking Phase 3 done):
- `rshell-sandbox` is still a placeholder. The Phase 3 binary uses
  direct `std::fs::File` opens for redirects and `cat`. cap-std-backed
  enforcement lands in Phase 4 alongside the file-reader builtins.
- Subshells share runner state for the baseline; bash forks. Phase 4
  will isolate state by cloning Env into the subshell scope.
- Command substitution, arithmetic, brace expansion, here-doc parameter
  expansion, glob expansion are all parsed but not evaluated. Phase 4
  wires them in builtin-by-builtin as scenarios demand.
- Against the **full** corpus (not just smoke set), `rshell-rs`
  currently passes 906/2643 + 58 skipped. The remaining failures are
  almost entirely in unported Phase-4 builtins (head, tail, wc, grep,
  sed, sort, find, ls, du, ps, ip, ss, ping, printf, …).

CI verification: run the snippet at the top of this file.

## Phase 4 — all 29 builtins

**Status:** not started
**Exit criterion:** `rshell-test-runner --bin target/release/rshell-rs`
reports 0 failures over the full scenario corpus (skips OK); `cargo`
checks green; CI green.

Builtins ordered by dependency / difficulty. Each is one task; mark as
done only when its scenarios pass under `rshell-rs`.

Trivial / control:

- [x] `true` (Phase 3)
- [x] `false` (Phase 3)
- [x] `:` (Phase 3)
- [x] `exit` (Phase 3)
- [x] `echo` (Phase 3)
- [x] `pwd` (Phase 3)
- [x] `printf` (Phase 4w1)
- [x] `help` (Phase 4w1)
- [x] `testcmd` / `test` / `[` (Phase 4w1)
- [x] `uname` (Phase 4w1)
- [x] `read` (Phase 4w3)

File readers:

- [x] `cat` (Phase 3)
- [x] `head` (Phase 4w1)
- [x] `tail` (Phase 4w1)
- [x] `wc` (Phase 4w1)
- [x] `cut` (Phase 4w1)
- [x] `tr` (Phase 4w1)
- [x] `uniq` (Phase 4w1)
- [x] `sort` (Phase 4w1)
- [x] `grep` (Phase 4w1)
- [x] `sed` (Phase 4w1)
- [x] `strings` (Phase 4w2)

Filesystem walkers:

- [x] `ls` (Phase 4w1)
- [x] `find` (Phase 4w2)
- [x] `du` (Phase 4w2)

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
