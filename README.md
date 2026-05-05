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

Every access path is default-deny:

| Resource             | Default                             | Opt-in / Opt-out                             |
|----------------------|-------------------------------------|----------------------------------------------|
| Command execution    | All commands blocked (exit code 127)| `AllowedCommands` (namespaced names), `AllowedCommandPatterns` (argv-prefix patterns), and `DeniedCommandPatterns` (deny-first carve-outs from the allow rules) |
| External commands    | Blocked (exit code 127)             | Provide an `ExecHandler`                     |
| Filesystem access    | Blocked                             | Configure `AllowedPaths` with directory list |
| Environment variables| Empty (no host env inherited)       | Pass variables via the `Env` option          |
| Output redirections  | Only `/dev/null` allowed (exit code 2 for other targets) | `>/dev/null`, `2>/dev/null`, `&>/dev/null`, `2>&1` |

**AllowedCommands** restricts which commands (builtins or external) the interpreter may execute. Commands must be specified with the `rshell:` namespace prefix (e.g. `rshell:cat`, `rshell:echo`). If not set, no commands are allowed.

**AllowedCommandPatterns** restricts execution to argv sequences shaped like `(command [, subcommand_path...])`. Each pattern is a non-empty token list; an invocation is admitted when:

1. `argv[0]` equals `pattern[0]` exactly (the command name).
2. The leading **structural** tokens of `argv[1..]` equal `pattern[1..]`, where structural tokens are derived by skipping flag tokens according to the [`CommandSpec`](#commandspec) registered for the command.

For example, with the built-in `ip` spec, pattern `["ip", "route"]` admits all of `ip route show`, `ip -4 route show`, `ip --brief route show` — the spec recognises `-4` and `--brief` as boolean global flags, so they're skipped during structural extraction. The same pattern blocks `ip addr show` and `ip link list` because the leading structural token after `ip` is `addr` / `link`, not `route`.

Patterns are matched **after shell expansion**, so command-substitution-derived values (`$(...)`) cannot bypass the check — the matcher sees the resolved argv that would be handed to exec. `AllowedCommands` and `AllowedCommandPatterns` are independent permit axes joined by union: a command is allowed if its name appears in `AllowedCommands` OR its argv satisfies any pattern.

**CommandSpec** describes the flag conventions of a single command so the matcher can distinguish flag tokens from structural tokens. The `ip` builtin is shipped with a default spec; integrators register additional specs (kubectl, git, docker, etc.) via the `CommandSpecs(map[string]CommandSpec{...})` option. Multi-token patterns whose command lacks a spec are rejected at `New()` — single-token patterns (matching only `argv[0]`) require no spec.

> Without a spec, the matcher would have to guess which argv tokens are flag values vs. positional arguments, and that guess is the only way `kubectl delete pod get` could ever masquerade as a `kubectl get` invocation. The spec-driven matcher closes that bypass by inspecting only the leading structural position for the subcommand, so positional arguments at later positions cannot satisfy pattern slots.

**DeniedCommandPatterns** blocks invocations whose argv satisfies any of the given patterns, regardless of whether `AllowedCommands` or `AllowedCommandPatterns` would otherwise admit the call. Denies are evaluated **first**: a deny match short-circuits the gate to a refusal even if every other axis would permit the invocation. Same shape and matching semantics as `AllowedCommandPatterns` (pattern is a token list, multi-token patterns require a registered `CommandSpec`, etc.). Used to express "allow X but carve out Y" policies — for example, `AllowedCommands={rshell:ip}` plus `DeniedCommandPatterns=[["ip","route"]]` permits `ip addr` and `ip link` but forbids `ip route`. The architectural property that allow patterns survive shell substitution applies here equally: a substitution that resolves at runtime to a denied argv is blocked.

**AllowedPaths** restricts all file operations to specified directories using Go's `os.Root` API (`openat` syscalls), making it immune to symlink traversal, TOCTOU races, and `..` escape attacks. Configured directories that cannot be opened (missing, not a directory, no permission) are skipped with a diagnostic message; by default these messages are flushed once to the runner's stderr at construction time. Callers that need to keep stderr clean of sandbox diagnostics can route them to a dedicated sink with `WarningsWriter(io.Writer)` or retrieve them programmatically via `Runner.Warnings()`.

> **Note:** The `ss` and `ip route` builtins bypass `AllowedPaths` for their `/proc/net/*` reads. Both builtins open kernel pseudo-filesystem paths (e.g. `/proc/net/tcp`, `/proc/net/route`) directly with `os.Open` rather than going through the sandboxed opener. These paths are hardcoded in the implementation and are never derived from user input, so there is no sandbox-escape risk. However, operators cannot use `AllowedPaths` to block `ss` from enumerating local sockets or `ip route` from reading the routing table — these reads succeed regardless of the configured path policy.

**ProcPath** (Linux-only) overrides the proc filesystem root used by the `ps` builtin (default `/proc`). This is a privileged option set at runner construction time by trusted caller code — scripts cannot influence it. Access to the proc path is intentionally not subject to `AllowedPaths` restrictions, since proc is a read-only virtual filesystem that does not expose host data under the normal file hierarchy.

## Shell Features

Inside rshell, run `help` to list supported feature categories, a concise unsupported-feature summary, and enabled commands. Use `help <feature|command>` for details about a specific rshell feature or command.

See [SHELL_FEATURES.md](SHELL_FEATURES.md) for the complete list of supported and blocked features.

## Platform Support

Linux, macOS, and Windows.

## Testing

**900+ YAML-driven test scenarios** cover builtins, shell features, and security restrictions.

```
tests/scenarios/
├── cmd/          # builtin command tests (echo, cat, grep, head, tail, test, uniq, wc, ...)
└── shell/        # shell feature tests (pipes, variables, control flow, ...)
```

By default, each scenario is executed twice: once in rshell and once in a real bash shell, ensuring output parity with POSIX behavior. Scenarios that test rshell-specific restrictions (blocked commands, readonly enforcement, etc.) opt out of the bash comparison.

```bash
go test ./...
```

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
