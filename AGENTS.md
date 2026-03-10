# Restricted Shell Interpreter

## Overview

This is a minimal bash/POSIX like shell interpreter.
Safety is the primary goal.

This shell is intended to be used by AI Agents.

## Platform Support

The shell is supported on Linux, Windows and macOS.

## Documentation

- `README.md` and `SHELL_FEATURES.md` must be kept up to date with the implementation.

## CRITICAL: Bug Fixes and Bash Compatibility

- **ALWAYS prioritise fixing the shell implementation to match bash behaviour over changing tests to match the current (incorrect) shell output.** Never "fix" a failing test by updating its expected output to match broken shell behaviour — fix the shell instead.
- Only deviate from bash behaviour when the shell is intentionally different (e.g. sandbox restrictions, blocked commands, readonly enforcement).

## Testing

- Before submitting any change that touches `tests/scenarios/` or builtin implementations, run the bash comparison tests locally. These are skipped by default and require Docker:
  ```
  RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
  ```
  The test suite runs all scenarios against `debian:bookworm-slim` (GNU bash + GNU coreutils) and compares output byte-for-byte. Only set `skip_assert_against_bash: true` in a scenario when the behavior intentionally diverges from bash (e.g. sandbox restrictions, blocked commands).

- In test scenarios, use `expect.stderr` when possible instead of `stderr_contains`.
- Test scenarios are asserted against bash by default. Only set `skip_assert_against_bash: true` for features that intentionally diverge from standard bash behavior (e.g. blocked commands, restricted redirects, readonly enforcement).
- When expected output differs on Windows (e.g. path separators `\` vs `/`), use Windows-specific assertion fields:
  - `stdout_windows` / `stderr_windows` — override `stdout` / `stderr` on Windows.
  - `stdout_contains_windows` / `stderr_contains_windows` — override `stdout_contains` / `stderr_contains` on Windows.
  - If the Windows field is not set, the non-Windows field is used as fallback.
