# Simplification and Improvement Proposals

Scope reviewed:

- `interp/`
- `cmd/rshell/`
- `tests/scenarios_test.go`
- `README.md`, `SHELL_FEATURES.md`, `SHELL_COMMANDS.md`

## Summary

The project is already small and readable, but several core concerns are still tightly coupled:

- runner configuration and runtime state are mixed together
- public API, internal behavior, and documentation are not fully aligned
- error handling is spread across many call sites and partly string-driven
- test infrastructure contains avoidable duplication

The proposals below are ordered roughly by payoff.

## 1. Split `Runner` configuration from execution state

**Priority:** High
**Main files:** `interp/api.go`, `interp/runner.go`, `interp/runner_exec.go`, `interp/runner_expand.go`

### Why

`Runner` currently mixes long-lived configuration with per-run mutable state:

- configuration-like fields: `Env`, handlers, sandbox, original stdio/dir
- execution state: exit codes, loop control flags, expand config, current stdio, current dir, filename

That makes lifecycle code more complex than it needs to be.

### Evidence

- `interp/api.go` defines a large `Runner` with both config and state in one struct.
- `Reset()` manually reconstructs a fresh `Runner` value and selectively copies fields.
- `subshell()` contains another manual copy path and even includes a `Keep in sync with the Runner type` comment.

### Proposal

Introduce a clear split such as:

- `runnerConfig` for immutable/shared settings
- `runnerState` for per-execution mutable state

Then make:

- `Reset()` reinitialize only `runnerState`
- `subshell()` clone only state that actually needs cloning
- `Runner` a small wrapper around config + current state

### Expected benefit

- less manual copying
- lower risk when adding fields
- easier reasoning about shell lifecycle
- simpler tests for reset/subshell behavior

## 2. Make shell capabilities explicit in the public API

**Priority:** High
**Main files:** `interp/api.go`, `interp/handler.go`, `interp/allowed_paths.go`, `README.md`, `SHELL_FEATURES.md`

### Why

The documentation describes opt-in execution and file access via handlers, but the public API does not expose that model cleanly.

### Evidence

- The only exported `RunnerOption`s are `Env`, `StdIO`, and `AllowedPaths`.
- There is no exported option for `ExecHandler`, `OpenHandler`, or `ReadDirHandler`.
- Internal tests mutate `runner.execHandler` directly, which external callers cannot do.
- `Reset()` forces `noExecHandler()` when `openHandler` is nil, which couples filesystem policy and execution policy in a surprising way.
- Docs mention that external commands can be enabled and that `AllowedPaths` also affects execution, but the current public behavior is effectively “exec is always blocked.”

### Proposal

Add explicit options such as:

- `WithExecHandler(...)`
- `WithOpenHandler(...)`
- `WithReadDirHandler(...)`

Or expose a single capability object:

```go
interp.WithCapabilities(interp.Capabilities{
    Exec: ...,
    Open: ...,
    ReadDir: ...,
})
```

Also define, in one place, how `AllowedPaths` composes with those handlers.

### Expected benefit

- API matches documentation
- less hidden behavior in `Reset()`
- easier future extension
- fewer test-only workarounds

## 3. Replace string-based error classification with typed shell errors

**Priority:** High
**Main files:** `interp/runner_expand.go`, `interp/runner.go`, `interp/vars.go`, `interp/handler.go`, `interp/builtins/*`

### Why

Error printing and exit-code decisions are spread across many locations, and some behavior depends on string inspection rather than typed semantics.

### Evidence

- `expandErr()` checks `strings.HasSuffix(errMsg, "not supported")`.
- printing happens in multiple places: validation, expansion, open failures, variable assignment, no-exec handler, and builtins.
- fatal vs non-fatal handling is encoded differently depending on the path.

### Proposal

Define a small typed error model, for example:

- validation error
- usage error
- path error
- expansion error
- internal bug/error

Then centralize:

- whether the message should be printed
- whether the shell exits immediately
- which exit code should be used

### Expected benefit

- removes fragile string matching
- makes behavior easier to test
- creates one consistent diagnostic style
- simplifies future feature additions

## 4. Break validation into feature-focused validators

**Priority:** Medium-High
**Main files:** `interp/validate.go`

### Why

`validateNode()` is currently doing several jobs at once:

- walking the AST
- blocking unsupported command nodes
- validating parameter expansions
- validating assignments
- validating redirects
- tracking heredoc words to special-case tilde handling

The logic is correct in spirit, but it is becoming a policy blob.

### Evidence

- `interp/validate.go` is one large switch over many syntax node types.
- heredoc bookkeeping and tilde policy are embedded directly in the walker.
- the file mixes traversal logic with feature rules and user-facing error text.

### Proposal

Keep one top-level walk, but delegate to small helpers by concern, for example:

- `validateCommandNode(...)`
- `validateWordNode(...)`
- `validateRedirect(...)`
- `validateParamExp(...)`
- `validateAssign(...)`

If the supported subset grows, consider a rule table or validator struct rather than extending one switch further.

### Expected benefit

- smaller review surface per feature
- clearer policy boundaries
- easier onboarding for contributors
- lower chance of subtle regressions when enabling new syntax

## 5. Simplify redirection and heredoc flow

**Priority:** Medium
**Main files:** `interp/runner_redir.go`, `interp/api.go`

### Why

The redirection code mixes several concerns:

- heredoc quoting rules
- `<<` vs `<<-`
- conversion of readers to `*os.File`
- shell stdin replacement
- resource cleanup

That makes the code harder to evolve than necessary.

### Evidence

- `hdocReader()` contains both semantic processing and transport mechanics.
- `redir()` has separate branches for heredoc, empty heredoc, and normal input redirection.
- `stdinFile()` in `api.go` is another piece of the same lifecycle, but it lives elsewhere.

### Proposal

Refactor into smaller building blocks:

- a pure heredoc builder that returns bytes/string content
- a single helper that turns a reader into shell stdin
- a small redirect dispatcher that only selects behavior

Keep POSIX-sensitive rules intact, but isolate them from pipe/file plumbing.

### Expected benefit

- easier to test heredoc behavior separately
- less branching in `redir()`
- cleaner ownership of close/cleanup logic

## 6. Extract shared test utilities and make scenario execution deterministic

**Priority:** Medium-High
**Main files:** `tests/scenarios_test.go`, `interp/allowed_paths_test.go`, `interp/allowed_paths_internal_test.go`, `cmd/rshell/main_test.go`

### Why

The test suite is valuable, but there is repeated setup and runner code that could be centralized.

### Evidence

- `setupTestDir()` and `setupTestDirIn()` duplicate fixture creation logic.
- `runScript()` and `runScriptInternal()` duplicate parser/runner/exit-code plumbing.
- CLI tests have another set of runner wrappers.
- `tests/scenarios_test.go` is nearly 400 lines and handles loading, setup, execution, docker orchestration, and assertions in one file.
- scenario grouping uses maps, so test enumeration order is not explicitly deterministic.

### Proposal

Create a small `tests/testutil` layer for:

- fixture creation
- runner creation
- script parsing/execution
- exit-code extraction
- scenario loading

Also:

- return sorted slices instead of ranging over maps for scenario groups
- sort environment keys when generating the bash runner script

### Expected benefit

- less duplicated test plumbing
- more reproducible test order and generated scripts
- easier addition of new scenario dimensions

## 7. Make intentional behavior differences first-class in scenario metadata

**Priority:** Medium
**Main files:** `tests/scenarios_test.go`, `tests/scenarios/**/*.yaml`, `AGENTS.md`

### Why

The scenario suite already distinguishes some cases that should not be compared against the local shell, but the reason is not structured.

### Evidence

- there are currently 161 scenarios with `test_against_local_shell: false`
- there are currently 51 scenarios that use `stderr_contains` instead of exact stderr matching
- the contributor guidance in `AGENTS.md` is stricter than what the test metadata currently captures

### Proposal

Replace the boolean opt-out with explicit reasons, for example:

- `comparison: posix`
- `comparison: intentional-divergence`
- `comparison: platform-specific`

Optionally add a `divergence_reason` field for cases where rshell intentionally differs from bash/POSIX.

### Expected benefit

- easier audits of why bash comparison is disabled
- better separation between bugs and intentional restrictions
- simpler maintenance of contributor guidance

## 8. Align naming and docs with the actual package layout

**Priority:** Medium
**Main files:** `README.md`, `AGENTS.md`, `SHELL_COMMANDS.md`, repository layout

### Why

The docs still talk about `pkg/shell`, but the current implementation lives under `interp/` and the CLI lives under `cmd/rshell`.

### Evidence

- a reader following the docs will look for `pkg/shell`, which does not exist in this checkout
- `SHELL_COMMANDS.md` and `README.md` still reference `pkg/shell`
- the mismatch is large enough that it already affects navigation and task framing

### Proposal

Pick one canonical naming scheme and use it everywhere. For example:

- public concept name: `rshell`
- library package: `interp`
- docs explicitly say “historically referred to as pkg/shell” only if needed

Also update any documentation that mentions unsupported public extension points.

### Expected benefit

- easier onboarding
- fewer incorrect file references
- tighter alignment between docs, code, and user expectations

## 9. Reduce panic/recover as part of normal control flow

**Priority:** Medium
**Main files:** `interp/api.go`, `interp/handler.go`, `interp/vars.go`

### Why

The code currently uses panics for several invariant violations and then relies on `Run()` to recover them into an “internal error”. That is acceptable for impossible states, but the surface is broader than it needs to be.

### Evidence

- `Run()` has a blanket recover block
- `HandlerCtx()` panics if context wiring is wrong
- `setVarWithIndex()` and `assignVal()` panic when validation should have rejected the input
- `Reset()` panics when `Runner` was not created with `New()`

### Proposal

Keep panics only for true programmer bugs, and use a small internal error type for other invariant failures when practical.

In particular, consider replacing blanket recover with recovery that wraps a dedicated internal error type or annotates the panic source more clearly.

### Expected benefit

- easier debugging when invariants fail
- clearer separation between user errors and implementation bugs
- less hidden control flow

## Suggested order of execution

If only a few changes should be tackled first, I would prioritize them in this order:

1. split `Runner` config/state
2. expose explicit handler/capability options
3. centralize typed error handling
4. extract shared test utilities
5. align docs and naming

Those five changes would simplify the core architecture the most without requiring a major feature expansion.