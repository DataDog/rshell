# Contributing to rshell

First of all, thanks for contributing!

## Submitting Issues

Use the GitHub issue tracker to report bugs or request features. Before opening a new issue, search existing issues to avoid duplicates.

## Setup

```
make build                                           # build the ./rshell binary
./rshell --allow-all-commands -c 'help'              # run the shell locally

# or compile and run with:
go run ./cmd/rshell --allow-all-commands -c 'help'
```

## Core Principle: Match Bash Behavior

This shell must behave like bash/POSIX shells. If the shell's output differs from bash, **fix the shell** — never "fix" a test by changing its expected output to match broken behavior. Only diverge intentionally (e.g. sandbox restrictions, blocked commands).

## Adding a Builtin

Use the `implement-posix-command` Claude Code skill (`.claude/skills/implement-posix-command/`) — it covers flag parsing, sandboxing rules, and test scaffolding end to end.
See `docs/RULES.md` for the underlying implementation rules.

## Testing

- `make test` — Go unit tests.
- `make test_against_bash` — runs `tests/scenarios/*.yaml` against real bash in Docker and diffs output byte-for-byte. Required before submitting any change touching `tests/scenarios/` or a builtin.
- Prefer adding a scenario test (`tests/scenarios/`) over a Go test; only use Go tests when a scenario cannot express the required behavior.

## Code Review

Before opening a PR (or once CI is green), self-review with the `review-fix-loop` Claude Code skill — it runs the review, fixes findings, and re-reviews until clean.

If your change touches `analysis/` (the import allowlists), it must be reviewed by a human, who will add the `verified/analysis` label — this cannot be automated.

## Pull Requests

To submit your changes:

1. Fork the repository.
2. Create a topic branch from `main`.
3. Make your changes — add tests for any new functionality.
4. Run `make fmt` and `make test` to ensure the code is formatted and all tests pass.
5. Push your branch and open a pull request against `main`.

Keep your pull requests focused on a single change to make review easier.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Write tests for new functionality and bug fixes.
- Keep changes minimal and focused.

## License

By contributing to this project you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
