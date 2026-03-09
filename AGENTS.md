# pkg/shell — Safe Shell Interpreter

## Overview

This is a minimal bash/POSIX like shell interpreter.
Safety is the primary goal.

This shell is intended to be used by AI Agents.

## Platform Support

The shell is supported on Linux, Windows and macOS.

## Documentation

- `README.md` and `SHELL_FEATURES.md` must be kept up to date with the implementation.

## Testing

- In test scenarios, use `expect.stderr` when possible instead of `stderr_contains`.
- Test scenarios are asserted against bash by default. Only set `skip_assert_against_bash: true` for features that intentionally diverge from standard bash behavior (e.g. blocked commands, restricted redirects, readonly enforcement).
- When expected output differs on Windows (e.g. path separators `\` vs `/`), use Windows-specific assertion fields:
  - `stdout_windows` / `stderr_windows` — override `stdout` / `stderr` on Windows.
  - `stdout_contains_windows` / `stderr_contains_windows` — override `stdout_contains` / `stderr_contains` on Windows.
  - If the Windows field is not set, the non-Windows field is used as fallback.
