# auto-improve-skills guardrails

Treat this directory as the Go project root for auto-improve-skills work.

- Run formatting and tests only from this directory.
- Use `make fmt` for formatting and `make test` for tests.
- Do not run parent-repository validation such as `make -C .. fmt`, `make -C .. test`, or `go test` from the parent checkout.
- Do not use `go test ../...` or other commands that intentionally traverse into the parent repository.
