# Audit: rshell vs the uutils vulnerability classes (2026-05)

## Context

Travis Thieman flagged a Slack thread linking to two posts about vulnerabilities found in the [uutils](https://github.com/uutils/coreutils) Rust reimplementation of GNU coreutils:

- [lcamtuf @ infosec.exchange — uutils security analysis](https://infosec.exchange/@lcamtuf/116517194178120536)
- [corrode.dev — Bugs Rust Won't Catch](https://corrode.dev/blog/bugs-rust-wont-catch/) — a 10-class checklist

The suggestion: feed these into Claude and check whether rshell has any of the same problems.

This document is the result of that audit. The audit is against the corrode.dev checklist (the lcamtuf post was not retrievable at audit time). No code changes were made; this document records what was checked and the conclusions.

## TL;DR

rshell is structurally well-defended against the corrode.dev classes. Most do not apply, and the rest are mitigated by the sandbox, the static analyzers under `analysis/`, and the rule that builtins must access the filesystem only through `callCtx.OpenFile` / `StatFile` / `LstatFile` / `ReadDir`.

No exploitable issues were found. Three behavioural observations are recorded under [§ Observations](#observations) for future reference; none are bugs.

## Methodology

For each of the 10 bug classes from corrode.dev, the audit looked for:

1. The Go-equivalent of the offending Rust pattern (e.g. `unwrap()` → `panic()`, `from_utf8_lossy` → `bytes.ToValidUTF8`, `File::create` → direct `os.Open`).
2. Whether project rules or static analyzers already prevent the pattern.
3. Concrete call sites worth a closer read.

Searches were run with `grep` over the non-test Go source. Spot reads followed for any non-obvious match.

## Per-class findings

### 1 + 10 — TOCTOU and path re-resolution

**Status:** mitigated.

- All builtin filesystem access routes through `callCtx.OpenFile` / `StatFile` / `LstatFile` / `ReadDir`, which delegates to `os.Root` (Go 1.24+, `openat`-based atomic path validation). See `allowedpaths/sandbox.go:316`.
- `docs/RULES.md` forbids direct `os` filesystem calls in builtins. `analysis/structural.go` enforces the `OpenFile`-then-`Close` pairing.
- The only direct `os.Open` calls outside the sandbox are documented bypasses for hardcoded kernel pseudo-files (`/proc/net/*` for `ss` and `ip route`, `/proc/sys/kernel/*` for `uname`-class data). Paths are not user-controllable. See `builtins/internal/procnetroute/`, `builtins/internal/procnetsocket/`, `builtins/internal/procsyskernel/`. The trade-off is documented in `CLAUDE.md` ("Security Design Decisions").
- The CLI script loader (`cmd/rshell/main.go:103`) opens the script the user explicitly passed on the command line. This is outside the sandbox by design.

### 2 — Path string equality vs filesystem identity

**Status:** mitigated.

- The sandbox canonicalises allowed roots via `filepath.EvalSymlinks` at startup (`allowedpaths/sandbox.go:86`), then resolves each call via `filepath.Clean` + `filepath.Rel` against the canonical root.
- `IsDevNull` (`allowedpaths/sandbox.go:303`) compares against the literal `/dev/null` (or case-insensitive `NUL` on Windows). This is a *strict* compare — it fails closed for variants like `/dev//null`, `/dev/./null`, or quoted forms (which the parser surfaces as `*syntax.DblQuoted`, not `*syntax.Lit`). The redirect gate at `interp/validate.go:250` only treats a `*syntax.Lit` whose value matches as `/dev/null`-allowed; everything else is rejected. There is no path string equality that grants access.

### 3 — Delayed permission setting

**Status:** N/A.

Builtins cannot write files at all. The sandbox rejects any open with `flag != os.O_RDONLY` (`allowedpaths/sandbox.go:317`).

### 4 — Lossy UTF-8 conversion on binary streams

**Status:** mitigated.

Go's `string(b)` is byte-faithful (it does not replace invalid bytes with U+FFFD the way Rust's `from_utf8_lossy` does). The codebase contains no `bytes.ToValidUTF8` calls in non-test code. The single `utf8.RuneError && size == 1` check (`builtins/wc/wc.go:310`) is the canonical "decode failed; treat as one byte" pattern, which is correct.

### 5 — Strict UTF-8 validation on untrusted input

**Status:** mitigated.

No `utf8.ValidString`-then-panic patterns. No rune iteration that would panic on non-UTF-8 filenames. No `expect`-equivalent on string conversions of input data.

### 6 — Discarded errors (`.ok()` / `let _ = …`)

**Status:** mitigated; one note.

- The only non-trivial error discards in non-test code are `_, _ = callCtx.Stdout.Write(…)` in `builtins/cut/cut.go:462` and `:475` — conventional broken-pipe handling. Output is capped to 1 MiB by the executor regardless. Most other builtins use `fmt.Fprint` to `callCtx.Stdout`, which similarly discards the return value.
- `builtins/internal/procnetsocket/procnetsocket_linux.go:261-262` and `builtins/internal/procinfo/procinfo_linux.go:171` parse `/proc` data with `_, _ = strconv.ParseUint(…)`. The kernel produces this data; treating an unexpected parse failure as zero is acceptable.
- `interp/api.go:273` ignores a reader `Close` error. Conventional.
- All other `_ =` patterns are intentional flag registrations (`fs.BoolP("…", …)`) or argument consumption (`getStringArg(…)` in printf).

### 7 — Behavioural divergence from GNU semantics (the `kill -1` class)

**Status:** mitigated.

- rshell has **no destructive builtins**. There is no `kill`, `rm`, `mv`, `cp`, `chmod`, `chown`, `mkdir`, `ln`, `dd`, or `tee`. The `kill -1` ambiguity (signal 1 vs. PID -1) cannot recur because the operation does not exist.
- Bash compatibility is enforced by `tests/scenarios/`, which runs every scenario against `debian:bookworm-slim` bash by default. 686 scenarios use `skip_assert_against_bash: true`. A spot survey shows the opt-outs are dominated by stderr-text wording and intentional sandbox blocks. A defence-in-depth follow-up would audit those opt-outs as a batch (out of scope here).

### 8 — Cross-trust-boundary lookups (NSS in chroot, etc.)

**Status:** N/A.

rshell does not chroot, drop privileges, or perform user/group lookups after a privilege transition. There is no analogous trust boundary to misuse.

### 9 — Panic on untrusted input

**Status:** mitigated.

All eight `panic()` sites in non-test code are programmer-invariant assertions on registration or startup paths, not runtime input:

- `interp/handler.go:26` — context-key sanity check
- `analysis/analyzer.go:51` — malformed allowlist entry at analyzer build
- `builtins/features.go:154,157` — empty/duplicate feature name at registration
- `builtins/builtins.go:69,270,276` — duplicate or conflicting builtin name at registration
- `builtins/tr/tr.go:613` — odd-length `buildRange` pairs; all callers pass static literals (`'0','9','A','Z',…`)

Spot-checks of `slice[len(s)-1]` patterns in `find/expr.go:759`, `ls/ls.go:749`, `tr/tr.go:306`, `cut/cut.go:301`, `sed/parser.go:360` confirm explicit `len(s) > 0` (or equivalent emptiness) guards upstream of every indexing operation. Numeric parsing uses `strconv.Parse{Int,Uint,Float}` with errors returned; integer accumulators (`echo` octal/hex parsing) are bounded by `maxDigits` so overflow is statically impossible.

## Already-enforced defences

The audit surfaced several existing project-level defences that block these classes structurally, not just incidentally. They are worth listing because they are why the answers above are short:

- **`os.Root`-based sandbox.** All builtin FS access goes through `callCtx.OpenFile` / `StatFile` / `LstatFile` / `ReadDir`, backed by Go 1.24's `os.Root` (`openat`-based, atomic path validation, no symlink escape).
- **Read-only enforcement.** `Sandbox.Open` rejects any flag != `os.O_RDONLY` (`allowedpaths/sandbox.go:317`).
- **Static analyzers (`analysis/`).**
  - `OpenFileCloseAnalyzer` enforces that every `callCtx.OpenFile` result is closed in the same function.
  - `ScannerBufferAnalyzer` enforces that every `bufio.NewScanner` has a `.Buffer()` call setting a bounded line size.
  - Symbol allowlists in `analysis/symbols_*.go` mean any new external import or stdlib symbol must be explicitly approved with a justification.
- **Per-builtin line and buffer caps.** Every line-oriented builtin caps at 1 MiB per line (`cat`, `sort`, `sed`, `grep`, `head`, `uniq`, `cut`, `wc`, `tail`, `ss`); `tail` caps total buffer at 5 MiB; the executor caps total output at 1 MiB.
- **Bash-comparison testing.** Scenario tests are byte-for-byte compared against bash by default; opt-outs are explicit.
- **Two-pass `find -exec` allowlist.** Static command names are validated upfront (`builtins/find/find.go:187-195`); `{}`-substituted command names are validated at eval time after substitution (`builtins/find/eval.go:307`).

## Observations

These are not findings, but worth keeping in mind.

1. **`find -exec` does not prefix `./` to matched filenames** (`builtins/find/eval.go:288-294`). This mirrors GNU find. A matched file named `--evil` is passed verbatim to the exec'd command. The risk is contained because (a) only allow-listed commands run, and (b) `find -execdir` *does* prefix `./`. Operators selecting an allow-list for sensitive commands should prefer `-execdir`. A SHELL_FEATURES.md callout could make this explicit.

2. **`IsDevNull` is strict-literal** (`allowedpaths/sandbox.go:303`). `/dev//null`, `/dev/./null`, and quoted `>"/dev/null"` (parsed as `*syntax.DblQuoted`) all fail to be recognised as the null device and are rejected by the redirect gate. The behaviour fails closed; users may find it surprising but it is not a security bug.

3. **`AllowedPaths` does not gate `/proc/net/*` or `/proc/sys/kernel/*`** (documented in `CLAUDE.md`). Hardcoded paths, no user input. Operators cannot use `AllowedPaths` to block `ss` from enumerating local sockets or `ip route` from reading the routing table. Documented intentional trade-off.

## Limitations

- The lcamtuf infosec.exchange post was not retrievable at audit time — it likely enumerates concrete uutils CVEs that map to specific commands. If the post becomes available, a targeted re-audit against those specific commands would be valuable.
- The 686 scenarios with `skip_assert_against_bash: true` were sampled but not surveyed in full. A separate sweep of those opt-outs would round out the bash-divergence story.
