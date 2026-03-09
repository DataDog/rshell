# rshell

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

	"github.com/DataDog/rshell/interp"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	script := `echo "hello from rshell"`

	prog, _ := syntax.NewParser().Parse(strings.NewReader(script), "")

	runner, _ := interp.New(
		interp.StdIO(nil, os.Stdout, os.Stderr),
	)
	defer runner.Close()

	runner.Run(context.Background(), prog)
}
```

## Security Model

Every access path is default-deny:

| Resource             | Default                             | Opt-in                                       |
|----------------------|-------------------------------------|----------------------------------------------|
| External commands    | Blocked (exit code 127)             | Provide an `ExecHandler`                     |
| Filesystem access    | Blocked                             | Configure `AllowedPaths` with directory list |
| Environment variables| Empty (no host env inherited)       | Pass variables via the `Env` option          |
| Output redirections  | Blocked at validation (exit code 2) | Not configurable — always blocked            |

**AllowedPaths** restricts all file operations to specified directories using Go's `os.Root` API (`openat` syscalls), making it immune to symlink traversal, TOCTOU races, and `..` escape attacks.

## Shell Features

See [SHELL_FEATURES.md](SHELL_FEATURES.md) for the complete list of supported and blocked features.

## Platform Support

Linux, macOS, and Windows.

## Testing

Tests use a YAML scenario-driven framework in `tests/scenarios/`:

```
tests/scenarios/
├── cmd/          # builtin command tests (echo, cat, exit, ...)
└── shell/        # shell feature tests (pipes, variables, control flow, ...)
```

```bash
go test ./...
```

## License

[Apache License 2.0](LICENSE)
