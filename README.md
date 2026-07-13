<p align="center">
  <img src="assets/rshell-logo-text.png" alt="rshell logo" width="600"/>
</p>

# rshell - A Restricted Shell for AI Agents

[![CI](https://github.com/DataDog/rshell/actions/workflows/test.yml/badge.svg)](https://github.com/DataDog/rshell/actions/workflows/test.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A restricted shell interpreter for Go. Designed for AI agents that need to run shell commands safely.

## Install

```bash
go get github.com/DataDog/rshell
```

## Quick Start

```go
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/DataDog/rshell/interp"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	script := `echo "hello from rshell"`

	prog, _ := syntax.NewParser().Parse(strings.NewReader(script), "")

	runner, _ := interp.New(
		interp.StdIO(nil, os.Stdout, os.Stderr),
		interp.AllowedCommands([]string{"rshell:echo"}),
		interp.MaxExecutionTime(5*time.Second),
	)
	defer runner.Close()

	runner.Run(context.Background(), prog)
}
```

CLI usage also supports a whole-run timeout:

```bash
rshell --allow-all-commands --timeout 5s -c 'echo "hello from rshell"'
```

## Security Model

**CLI scope:** The `rshell` CLI is a development, debugging, and local validation harness for the interpreter. It is not intended to be exposed as a production execution boundary or as an interface for untrusted users. Production integrations should embed the Go API and set `AllowedCommands`, `AllowedPaths`, environment, timeout, and mode explicitly. Security review should treat the interpreter and embedding configuration as the security boundary, not the developer CLI.

Every access path is default-deny:

| Resource             | Default                             | Opt-in                                       |
|----------------------|-------------------------------------|----------------------------------------------|
| Command execution    | All commands blocked (exit code 127)| `AllowedCommands` with namespaced command list (e.g. `rshell:cat`) |
| System services      | All services and actions blocked    | `AllowedSystemServices` with exact service/action grants |
| External commands    | Blocked (exit code 127)             | Provide an `ExecHandler`                     |
| Filesystem access    | Blocked                             | Configure `AllowedPaths` with `PATH[:ro|:rw]` root specs |
| Environment variables| Empty (no host env inherited)       | Pass variables via the `Env` option          |
| Output redirections  | Only `/dev/null` allowed in read-only mode (exit code 2 for other targets); file-target redirects enabled in remediation mode, gated by `AllowedPaths` | `>/dev/null`, `2>/dev/null`, `&>/dev/null`, `2>&1`; in remediation mode: `>FILE`, `>>FILE`, `2>FILE`, `&>FILE`, `&>>FILE` |

**AllowedCommands** restricts which commands (builtins or external) the interpreter may execute. Commands must be specified with the `rshell:` namespace prefix (e.g. `rshell:cat`, `rshell:echo`). If not set, no commands are allowed.

**AllowedSystemServices** configures exact service/action grants for future system-service builtins. Service names are matched exactly without adding suffixes, resolving aliases, changing case, or otherwise normalizing them: `mysql` and `mysql.service` are different grants. The supported actions are `read`, `reload`, and `restart`. Empty names, whitespace, path-like names, and glob patterns are rejected. The policy defaults to denying every service, remains enforced when all commands are allowed, and requires remediation mode before any grant can be authorized.

```go
interp.AllowedSystemServices([]interp.SystemServiceControlGrant{
	{
		Service: "mysql.service",
		Actions: []interp.SystemServiceAction{
			interp.SystemServiceRestart,
			interp.SystemServiceReload,
			interp.SystemServiceRead,
		},
	},
})
```

The development CLI accepts the equivalent syntax through `--allowed-services mysql.service:restart+reload+read,nginx.service:read`. The policy and authorization capability are implemented, but `systemctl` and `journalctl` builtins are not yet available.

**AllowedPaths** restricts all file operations to specified directories using Go's `os.Root` API for reads and openat-based write handling for writes.

- **Sandbox mechanism:** Reads go through `os.Root`; writes are checked against the most-specific path mode and, on Unix, opened with a no-symlink `openat` walk. Files outside the allowlist cannot be opened, created, truncated, or appended to.
- **Permission suffix:** Path entries may end with `:ro` or `:rw` representing read-only and read-write modes, respectively; entries without a suffix default to read-only, and the suffix is stripped before path validation. In remediation mode, write operations are accepted only inside the most-specific matching `:rw` root.
- **Symlink policy:** A symlink pointing outside its `os.Root` is followed for reads but never for writes; on Unix, symlink components in write targets are rejected with `symlinks are not supported as write targets` rather than followed, eliminating the TOCTOU window where a malicious link target could be swapped between resolution and open.
- **Output redirections by mode:**
  - _Read-only mode (default):_ file-target output redirections (`>`, `>>`, `2>`, `&>`, `&>>`) are rejected at parse time (exit 2).
  - _Remediation mode:_ those redirections open through the same sandbox — writes inside `:rw` allowlist roots succeed; targets outside the allowlist or inside read-only roots fail with `permission denied` (exit 1).
- **Special targets:** The literal target `/dev/null` is always short-circuited to a discarded sink without going through the sandbox. `<>` (read-write open) is blocked in all modes.
- **Diagnostic messages:** Configured directories that cannot be opened (missing, not a directory, no permission) are skipped with a warning flushed once to the runner's stderr at construction time. Silence them or redirect them programmatically with `WarningsWriter(io.Writer)` or `Runner.Warnings()`.

> **Note:** The `ss`, `ip route`, and `df` builtins bypass `AllowedPaths` for their kernel-state reads. `ss` and `ip route` open `/proc/net/*` paths directly; `df` reads `/proc/self/mountinfo` (Linux) or calls `getfsstat(2)` (macOS), then issues `unix.Statfs(2)` against every kernel-reported mount point. These paths are hardcoded — never derived from user input — and `Statfs` returns metadata only (block / inode counts, filesystem type, block size). There is no sandbox-escape risk, but operators cannot use `AllowedPaths` to block `ss` from enumerating local sockets, `ip route` from reading the routing table, or `df` from reporting mount-table capacity — these reads succeed regardless of the configured path policy.

**ProcPath** (Linux-only) overrides the proc filesystem root used by the `ps` builtin (default `/proc`). This is a privileged option set at runner construction time by trusted caller code — scripts cannot influence it. Access to the proc path is intentionally not subject to `AllowedPaths` restrictions. To avoid leaking secrets passed as CLI arguments, `ps` does not read `/proc/<pid>/cmdline`; the `CMD` column reports only the process comm/executable name.

**RemediationMode** opts the runner into host-remediation mode, enabling file-target output redirections (`>`, `>>`, `2>`, `&>`, `&>>`) within `:rw` entries in the configured `AllowedPaths` and enabling remediation-only builtins such as `truncate` and `logrotate`. Remediation-only commands are rejected by builtin dispatch before argument parsing when this mode is disabled. Targets outside the allowlist or inside read-only roots are rejected with `permission denied` (exit 1); symlinked write targets are rejected with `symlinks are not supported as write targets`; `/dev/null` is always accepted. `<>` (read-write open) remains blocked in all modes. CLI flag: `--mode remediation` (default: `--mode read-only`). The `logrotate` builtin is an rshell-safe log truncation helper, not a full `logrotate(8)` replacement.

## Shell Features

Inside rshell, run `help` to list supported feature categories, a concise unsupported-feature summary, enabled commands, and the configured `AllowedPaths` sandbox roots grouped by read-only and read-write access (or a notice when none are configured). Use `help <feature|command>` for details about a specific rshell feature or command.

See [SHELL_FEATURES.md](SHELL_FEATURES.md) for the complete list of supported and blocked features.

## Platform Support

Linux, macOS, and Windows.

## Publishing Changes

After merging changes to `main` create a release by:

1. Navigate to the [Releases](https://github.com/DataDog/rshell/releases) page

2. Click "Draft a new release"

3. You can "Select a tag" using the dropdown or "Create a new tag"

   When creating a new tag, make sure to include the `v` prefix. For example, if the last release was v0.1.29, your release should be v0.1.30.

4. The release title should be the same as the version tag

5. Use "Generate release notes" to fill in the release description

6. Click "Publish release"

   This will create a git tag that can now be referenced in other repos. This will trigger go-releaser that will add installable artifacts to the release.

## License

[Apache License 2.0](LICENSE)
