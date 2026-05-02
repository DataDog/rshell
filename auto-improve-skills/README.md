# Auto-Improve Skills

Harness for improving `skills/remote-host-diagnostics/SKILL.md` with fixed benchmarks, nested `pi` runs, and git-tracked accepted iterations.

## Contents

- `program.md` — instructions given to researcher agents.
- `skills/remote-host-diagnostics/SKILL.md` — skill being tuned.
- `benchmarks/remote-host-diagnostics/` — public and holdout suites plus generated fixtures.
- `cmd/skillbench`, `cmd/skillfixtures`, `cmd/skilltrain` — Go CLIs. Run any command with `-h` for current flags and defaults.
- `runs/` — generated benchmark/training outputs, including per-iteration `SKILL.candidate.md`, `SKILL.previous.md`, and `SKILL.diff` artifacts from `skilltrain`.

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

## LLM context isolation

The harness starts nested `pi` agents with `--no-context-files` so they do not load `AGENTS.md`/`CLAUDE.md` from this directory, parent directories, or the global Pi agent directory. Benchmark/researcher runs also disable discovered extensions, prompt templates, and skills except for the explicitly supplied benchmark skill.

If you start `pi` manually inside this nested directory and want the same behavior, use `pi --no-context-files` (or `pi -nc`).
