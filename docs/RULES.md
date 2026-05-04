# Builtin Command Testing Rules

_[Preamble to be added]_

---

## Implementation Choices

### Flag Parsing

All flag parsing MUST use `github.com/spf13/pflag` (already a dependency in `go.mod`).
Do NOT write manual flag-parsing loops.

pflag handles POSIX/GNU conventions automatically:
- Long forms: `--lines=N` and `--lines N`
- Short compact forms: `-n3` (value glued to flag)
- Combined boolean short flags: `-qv`
- `--` end-of-flags separator

**Setup pattern:**
```go
fs := pflag.NewFlagSet("cmdname", pflag.ContinueOnError)
fs.SetOutput(io.Discard) // suppress pflag's own error output; format errors yourself
```

**Flags that need `+N` offset support** (e.g. `tail -n +5`): register as `StringP`, then
post-process the value with a helper that detects the `+` prefix.

**Error handling:** pflag parse errors (unknown flag, missing argument) should be written
to `r.stderr` with a `"cmdname: "` prefix and set `r.exitCode = 1; return nil`. This
matches POSIX command-failure semantics — a bad flag fails the command but does not abort
the script.

### Supported Flags Only

Commands MUST only implement the flags listed in their supported flag set. Any flag not
explicitly registered with pflag is automatically rejected with an "unknown flag" error
written to stderr and exit code 1. Do NOT add pre-scan loops or special-case logic to
reject specific flags by name — rely on pflag's unknown-flag handling instead.

### Help Flag

Every command MUST register `-h` / `--help` as a flag. When `--help` is passed:
- Print a usage line (`Usage: cmd [OPTION]... [FILE]...` or equivalent) to **stdout**
- Print a short description of what the command does to stdout
- Print all supported flags with brief descriptions via `fs.PrintDefaults()` to stdout
- Set exit code 0 and return

Do not write help output to stderr. Help is not an error.

### File Access — Safe Wrappers Only

Builtins MUST access the filesystem exclusively through `callCtx.OpenFile`. Never call
`os.Open`, `os.OpenFile`, `os.ReadFile`, `os.ReadDir`, `os.Stat`, `os.Lstat`, or any other
`os`-package filesystem function directly.

`callCtx.OpenFile` routes through the `AllowedPaths` sandbox (backed by `os.Root`), which
enforces path restrictions atomically via `openat` syscalls. Bypassing it — even for a
"harmless" stat or existence check — defeats the sandbox entirely.

```go
// CORRECT
f, err := callCtx.OpenFile(ctx, path, os.O_RDONLY, 0)

// WRONG — bypasses the sandbox
f, err := os.Open(path)
```

Using `os` constants (`os.O_RDONLY`, `os.FileMode`) and types (`*os.File` for stdin) is fine;
only the filesystem-accessing *functions* are forbidden.

---

## Implementation Rules

### File System Safety
- Commands MUST NOT write to any files on the system in any way
- Commands MUST NOT execute any files or external binaries on the system in any way
- Commands MUST NOT create, modify, or delete files, directories, or symlinks
- Commands MUST NOT follow symlinks during write operations (no writes = no risk, but verify)

### Memory Safety & Resource Limits
- Commands MUST use bounded buffers when reading input (never allocate based on untrusted input size)
- Commands MUST apply backpressure when reading from infinite streams (e.g., stdin from /dev/zero)
- Commands MUST limit memory consumption to prevent exhaustion attacks
- Commands MUST NOT load entire files into memory when line-by-line or chunked processing is viable
- Commands MUST handle very long lines (>1MB) without crashing or excessive memory use
- Commands MUST respect the global 1MB output limit (enforced by executor, but don't generate excess)

### Input Validation & Error Handling
- Commands MUST validate all numeric arguments (line counts, byte counts, field numbers) for overflow
- Commands MUST reject negative values where semantically invalid
- Commands MUST reject all disallowed flags with clear error messages
- Commands MUST fail safely on malformed or binary input (no crashes, no hangs)
- Commands MUST return proper exit codes (0 = success, 1 = error, as appropriate)
- Commands MUST write error messages to stderr, not stdout

### Special File Handling
- Commands MUST handle /dev/zero, /dev/random, and similar infinite sources safely (bounded reads, timeout respected)
- Commands MUST NOT block indefinitely when reading from FIFOs or pipes
- Commands MUST handle /proc and /sys files appropriately (short reads, non-seekable)
- Commands MUST handle non-regular files (directories, devices, sockets) with appropriate errors

### Regular Expression Safety (for grep, sed, etc.)
- Regex engines MUST use bounded execution to prevent ReDoS attacks
- Commands MUST timeout or limit backtracking on pathological regex patterns
- Commands MUST validate regex syntax before execution
- Commands SHOULD prefer linear-time regex engines (e.g., RE2) over backtracking engines where possible

### Path & Traversal Safety
- Commands MUST resolve relative paths correctly relative to working directory
- Commands MUST handle path traversal (../) without restriction (by design - rely on OS permissions)
- Commands MUST NOT be confused by absolute paths, multiple slashes, or . and .. components
- Commands MUST handle paths with special characters (spaces, newlines, unicode) correctly
- Commands MUST follow symlinks for read operations (by design - document this behavior)

### Concurrency & Race Conditions
- Commands MUST use atomic operations where ordering matters
- Commands MUST NOT be vulnerable to TOCTOU races (already mitigated by no-write policy)
- Commands MUST be safe for concurrent execution (no shared mutable state)

### Denial of Service Prevention
- Commands MUST respect the 30-second execution timeout (enforced by executor)
- Commands MUST NOT enter infinite loops on any input
- Commands MUST NOT cause excessive CPU usage through algorithmic complexity attacks
- Commands MUST NOT exhaust file descriptors or other system resources
- Commands MUST handle interruption (context cancellation) gracefully

### Goroutine Context Propagation
- Goroutines spawned during command or interpreter execution MUST propagate and respect context cancellation. Before any blocking I/O in a goroutine, check `ctx.Err()`. For multi-chunk writes, check `ctx.Err()` between chunks. Close the pipe write end (`pw.Close()`) on cancellation to unblock the reader and propagate termination.

### Integer Safety
- Commands MUST check for integer overflow in all arithmetic operations
- Commands MUST validate numeric conversions (string to int) and handle errors
- Commands MUST handle edge cases (INT_MAX, 0, negative numbers) correctly

### Output Consistency
- Commands MUST produce deterministic output for the same input
- Commands MUST NOT leak sensitive information through error messages or timing
- Commands MUST handle line endings consistently (\n, \r\n, \r)
- Commands MUST preserve or correctly handle binary data (non-UTF8)

### Testing Requirements
- Every dangerous flag MUST have a test verifying it is rejected
- Every supported flag MUST have correctness tests
- Edge cases (empty files, no trailing newline, single line) MUST be tested
- Error paths (missing file, invalid args) MUST be tested
- Security properties MUST be tested (path traversal, special files, large inputs)
- Integration with shell features (pipes, for-loops, globs) MUST be tested

### Platform Targeting

**Linux is the primary target.** macOS and Windows are not prioritised for now. Reviewers MUST NOT block PRs on hypothetical macOS or Windows breakage. When implementation decisions trade off cross-platform portability against Linux clarity or correctness, prefer Linux.

- Commands MUST work correctly on Linux. Linux behaviour is the source of truth.
- Commands MAY use Linux-only paths (`/dev/zero`, `/dev/null`, `/dev/random`, `/proc`, `/sys`) without platform guards.
- Commands MAY use `//go:build linux` build tags freely when an implementation is inherently Linux-specific.
- Commands SHOULD use the `filepath` package for path operations — it is cheap correctness and costs nothing.
- Commands SHOULD handle CRLF (`\r\n`) line endings in input data, since Linux files often contain CRLF from cross-platform editors.
- Tests SHOULD target Linux. Do not add macOS- or Windows-specific test fixtures, build tags, or assertion overrides proactively — wait until a real bug on those platforms is reported.

---

## Sources & References

Research based on:

**General Security:**
- [GNU Coreutils CVE Database](https://www.cvedetails.com/product/5075/GNU-Coreutils.html)
- [OWASP ReDoS Attack](https://owasp.org/www-community/attacks/Regular_expression_Denial_of_Service_-_ReDoS)
- [Symlink TOCTOU Attacks](https://medium.com/@instatunnel/symlink-attacks-when-file-operations-betray-your-trust-986d5c761388)
- [Wikipedia: Time-of-check to time-of-use](https://en.wikipedia.org/wiki/Time-of-check_to_time-of-use)
- [Special Device Files](https://en.wikipedia.org/wiki//dev/zero)

**Path Handling:**
- [Go filepath Package Documentation](https://pkg.go.dev/path/filepath)
- [Line Ending Types: LF vs CRLF vs CR](https://www.baeldung.com/linux/line-breaks-types)
