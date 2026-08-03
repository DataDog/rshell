<p align="center">
  <img src="assets/rshell-logo-text.png" alt="rshell" width="420">
</p>

# rshell

[![CI](https://github.com/DataDog/rshell/actions/workflows/test.yml/badge.svg)](https://github.com/DataDog/rshell/actions/workflows/test.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A default-deny shell interpreter for Go, built for AI agents that need a bounded Bash/POSIX-like command surface.

> [!IMPORTANT]
> The CLI is a development and local-validation tool, not a production security boundary. Production integrations should embed the Go package and explicitly configure commands, paths, environment, timeouts, and execution mode.

## Install

```bash
go get github.com/DataDog/rshell/interp
```

For the optional development CLI:

```bash
go install github.com/DataDog/rshell/cmd/rshell@latest
```

## Quick start

A minimal embedded runner (imports omitted):

```go
func runScript(ctx context.Context) error {
	program, err := interp.ParseScript(`echo "hello from rshell"`, "")
	if err != nil {
		return err
	}

	runner, err := interp.New(
		interp.StdIO(nil, os.Stdout, os.Stderr),
		interp.AllowedCommands([]string{"rshell:echo"}),
		interp.MaxExecutionTime(5*time.Second),
	)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.Run(ctx, program)
}
```

The same command through the development CLI:

```bash
rshell --allowed-commands rshell:echo --timeout 5s -c 'echo "hello from rshell"'
```

## Security model

Policy is layered and default-deny:

| Surface | Default | Explicit opt-in |
|---|---|---|
| Commands | Denied | `AllowedCommands` with namespaced entries such as `rshell:cat` |
| Filesystem | Denied | `AllowedPaths` roots, optionally suffixed with `:ro` or `:rw` |
| Environment | Empty; the host environment is not inherited | `Env` |
| Writes and remediation commands | Disabled | `WithMode(ModeRemediation)` plus a matching `:rw` path or capability grant |
| Systemd | All units and actions denied | Exact unit/action grants through `AllowedSystemServices`; `systemctl` also requires remediation mode |

`AllowedCommands` authorizes command names; it does not install an external executor. The default handler rejects unknown commands and host binaries. Read-only mode is the default; remediation mode enables only the separately authorized write and host-remediation surfaces.

Some inspection builtins read fixed kernel interfaces outside `AllowedPaths`, and trusted systemd target paths intentionally bypass the filesystem sandbox. Their platform limits, data exposure, and authorization rules are documented in the [feature reference](SHELL_FEATURES.md).

## Features and platforms

Run `help` inside rshell for the commands and policy active on a runner, or `help <command>` for command-specific details. See [SHELL_FEATURES.md](SHELL_FEATURES.md) for the complete supported and blocked feature matrix.

The interpreter supports Linux, macOS, and Windows. Some host-inspection builtins are platform-specific; the feature reference calls those out individually.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, testing, and pull request guidance. Security-sensitive builtin implementation rules live in [docs/RULES.md](docs/RULES.md).

## License

[Apache License 2.0](LICENSE)
