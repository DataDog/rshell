# Auto-Improve Skills

Harness for improving `skills/remote-host-diagnostics/SKILL.md` with fixed benchmarks, nested `pi` runs, and git-tracked accepted iterations.

## Contents

- `program.md` — instructions given to researcher agents.
- `skills/remote-host-diagnostics/SKILL.md` — skill being tuned.
- `benchmarks/remote-host-diagnostics/` — public and holdout suites plus generated fixtures.
- `cmd/skillbench`, `cmd/skillfixtures`, `cmd/skilltrain` — Go CLIs. Run any command with `-h` for current flags and defaults.
- `runs/` — generated benchmark/training outputs.

## Usage

Run commands from the repository root. Ensure `pi` is installed/authenticated and `./rshell` exists (`make build` if needed).

Benchmark:

```sh
go run ./auto-improve-skills/cmd/skillbench
```

Training loop:

```sh
go run ./auto-improve-skills/cmd/skilltrain -iters 3 -judge
```

Generate fixtures only:

```sh
go run ./auto-improve-skills/cmd/skillfixtures
```

Generated fixtures and run outputs are intentionally not committed.
