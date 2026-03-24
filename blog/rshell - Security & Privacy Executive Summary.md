# rshell: Security & Privacy Executive Summary

**Author**: Claude Opus 4.6 via [Travis Thieman](mailto:travis.thieman@datadoghq.com)  
**Date**: Mar 18, 2026  
**Project**: rshell — Restricted Shell Interpreter for AI Agents   
**Repository**: [github.com/DataDog/rshell](https://github.com/DataDog/rshell) @ 0.0.4

---

## Purpose

rshell is a minimal, bash-compatible shell interpreter written in Go, designed to be embedded in AI agent workflows. Its primary design goal is **safety**: it provides agents with familiar shell capabilities while preventing access to host resources, sensitive data, and dangerous operations. Every access path — filesystem, commands, environment, and network — is blocked by default and must be explicitly opted in by the caller.

---

## 1\. Security Architecture: Default-Deny by Design

rshell follows a **default-deny security model**. When a new shell session is created, it starts with no permissions. The embedding application must explicitly grant access to each resource category:

| Resource | Default | Opt-In Mechanism |
| :---- | :---- | :---- |
| Commands | All blocked | Caller provides a namespaced allowlist (e.g. `rshell:cat`) |
| External binaries | Blocked | Caller must provide a custom execution handler |
| Filesystem access | Blocked | Caller specifies allowed directory paths |
| Environment variables | Empty | Caller provides key-value pairs explicitly |
| Output redirections | Blocked | Only `/dev/null` is permitted |

This means an AI agent running inside rshell cannot access anything the embedding application hasn't explicitly authorized. There is no way to "break out" of the shell to access the broader host system through standard shell operations.

For full details, see the [README security model table](https://github.com/DataDog/rshell#security-model).

---

## 2\. Filesystem Sandbox

File access is mediated through Go 1.24's `os.Root` API, which provides **kernel-level path confinement** using atomic `openat` syscalls. This gives rshell several important guarantees:

- **No directory traversal**: Attempts to access files outside allowed directories (e.g. `../../etc/passwd`) are rejected at the syscall level.  
- **Symlink safety**: Symbolic links are resolved safely within the allowed directory boundaries. An attacker cannot plant a symlink that escapes the sandbox.  
- **No race conditions**: Path validation and file opening happen atomically in a single syscall, eliminating time-of-check/time-of-use (TOCTOU) vulnerabilities.  
- **Read-only enforcement**: All file operations are restricted to read-only access. Write operations are rejected regardless of the path.

Additionally, directory listing operations are capped at **100,000 entries** to prevent memory exhaustion from adversarial directory structures.

Implementation: [`allowedpaths/sandbox.go`](https://github.com/DataDog/rshell/blob/main/allowedpaths/sandbox.go)

---

## 3\. Blocked Shell Features

Many standard shell features that could be used for exploitation are **blocked at the syntax level** before any code executes. rshell parses scripts into an abstract syntax tree (AST) and rejects dangerous constructs with a clear error. Blocked features include:

- **Loops** (`while`, `until`) — prevents infinite loops and resource exhaustion  
- **Function declarations** — prevents code redefinition attacks  
- **Background execution** (`&`, `coproc`) — prevents process spawning  
- **Arithmetic expansion** (`$(( ))`) — prevents expression injection attacks  
- **Process substitution** (`<()`, `>()`) — prevents hidden subprocess creation  
- **Tilde expansion** (`~`) — prevents disclosure of host user information  
- **Write redirections** (`>`, `>>`) — prevents file creation or modification

A comprehensive feature matrix is maintained in [SHELL\_FEATURES.md](https://github.com/DataDog/rshell/blob/main/SHELL_FEATURES.md).

Implementation: [`interp/validate.go`](https://github.com/DataDog/rshell/blob/main/interp/validate.go)

---

## 4\. Import Allowlist — Static Analysis of Builtin Commands

Every builtin command (e.g. `cat`, `grep`, `head`) is implemented purely in Go with **no external binary execution**. To ensure these implementations cannot introduce unsafe behavior, rshell maintains a **static symbol allowlist** that restricts which Go standard library functions builtins are permitted to use.

Key properties of this system:

- **Every symbol requires a safety justification**: Each allowed function has a comment explaining why it is safe to use (e.g., `bufio.NewScanner` — "line-by-line input reading; no write or exec capability").  
- **Permanently banned packages**: The `reflect` and `unsafe` packages are categorically prohibited, as they can circumvent Go's type safety and memory protections.  
- **Human-reviewed changes**: Any modification to the allowlist requires a human reviewer to manually apply a `verified/allowed_symbols` label on the pull request. This label **cannot be applied by automation or CI** — it requires deliberate human approval.  
- **CI enforcement**: A dedicated [GitHub Actions workflow](https://github.com/DataDog/rshell/blob/main/.github/workflows/allowed-symbols.yml) blocks merging if allowlist changes lack the required label.

Implementation: [`allowedsymbols/`](https://github.com/DataDog/rshell/tree/main/allowedsymbols)

---

## 5\. Network Command Restrictions

rshell includes several network diagnostic commands (`ping`, `ip`, `ss`) that are intentionally limited to **read-only, non-destructive operations**:

- **`ping`**: Flood mode (`-f`), broadcast (`-b`), custom packet sizes (`-s`), and interface binding (`-I`) are all blocked. Count, wait, and interval values are clamped to safe ranges. Multicast and broadcast destination addresses are rejected.  
- **`ip`**: Only read-only operations (`addr show`, `link show`) are permitted. All write operations (`addr add`, `link set`, `link del`) and namespace operations are blocked.  
- **`ss`**: Socket statistics are read-only. Process ID disclosure (`-p`), connection killing (`-K`), and file-read vectors (`-F`) are blocked.

These commands read kernel state directly through safe system interfaces — they do not provide the ability to make outbound network connections or modify network configuration.

---

## 6\. Memory & Resource Safety

rshell implements multiple layers of protection against denial-of-service through resource exhaustion:

| Resource | Cap | Purpose |
| :---- | :---- | :---- |
| Individual variable size | 1 MiB | Prevents memory bombs via assignment |
| Command substitution output | 1 MiB | Prevents capture of unbounded output |
| Line buffer per command | 1 MiB | Prevents single-line memory exhaustion |
| Directory listing entries | 100,000 | Prevents glob-based memory exhaustion |
| Integer arguments | Clamped to 2³¹−1 | Prevents huge allocation requests |

All I/O loops check for context cancellation, ensuring operations can be terminated by the embedding application at any time.

---

## 7\. Security Testing

rshell employs a multi-layered testing strategy specifically focused on security:

### Penetration Tests

Dedicated `*_pentest_test.go` files target known attack vectors for each builtin command. These tests verify that exploit techniques catalogued by [GTFOBins](https://gtfobins.github.io/) — a public database of Unix binary exploitation techniques — are blocked. Examples include batch file-read vectors in `ip`, shell escape via `netns exec`, and argument injection through variable expansion.

### Fuzz Testing

A continuous [fuzzing pipeline](https://github.com/DataDog/rshell/blob/main/.github/workflows/fuzz.yml) runs on every pull request and push to main. This includes **differential fuzzing** that compares rshell output against real bash, catching behavioral divergences that could indicate security-relevant bugs.

### Bash Comparison Tests

All YAML-based test scenarios are validated against real GNU bash running in Docker, ensuring byte-for-byte output parity. This catches cases where rshell might behave unexpectedly compared to the shell it emulates.

### CI Pipeline

All security checks run automatically on every pull request:

- Import allowlist enforcement (with human review gate)  
- Full test suite including pentest scenarios  
- Fuzz corpus execution  
- `gofmt` formatting verification

---

## 8\. Privacy Considerations

rshell is designed to minimize information disclosure:

- **No host environment inheritance**: The shell starts with an empty environment. Host environment variables (which may contain secrets, API keys, or credentials) are never accessible unless the caller explicitly passes them in.  
- **No user information disclosure**: Tilde expansion (`~`, `~user`) is blocked, preventing discovery of host usernames or home directory paths.  
- **No process disclosure by default**: The `ps` command shows only processes relevant to the shell session. The `ss` command blocks the `-p` flag that would reveal process IDs associated with network connections.  
- **Stderr isolation**: Error output from command substitutions is not captured in variables, preventing information leakage through error channels.  
- **Read-only filesystem**: Even when file access is granted, it is strictly read-only — the shell cannot create, modify, or delete files.

---

## 9\. Dependency Posture

rshell maintains a minimal dependency footprint to reduce supply chain risk:

- **Core parser**: `mvdan.cc/sh` — well-established open-source POSIX/bash parser  
- **Flag parsing**: `github.com/spf13/pflag` — standard Go ecosystem library  
- **Network diagnostics**: `github.com/prometheus-community/pro-bing` — ICMP implementation for ping  
- **System calls**: `golang.org/x/sys` — official Go extended syscall library

All other dependencies are test-only (`testify`, `yaml`). The project has no web framework, database driver, or cloud SDK dependencies.

---

## Summary

rshell provides AI agents with a familiar shell interface while maintaining strict security boundaries. Its defense-in-depth approach — combining a default-deny architecture, kernel-level filesystem sandboxing, static analysis of builtin implementations, human-gated allowlist reviews, and comprehensive security testing — ensures that agents operate within well-defined boundaries. The project is designed so that the embedding application retains full control over what resources are accessible, with no ambient authority granted by default.

For questions or to report security issues, see the [repository](https://github.com/DataDog/rshell).

