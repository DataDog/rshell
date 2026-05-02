# Auto-Improve Skills

Harness for improving `skills/remote-host-diagnostics/SKILL.md` with fixed benchmarks, nested `pi` runs, and git-tracked accepted iterations.

## Contents

- `program.md` — instructions given to researcher agents.
- `skills/remote-host-diagnostics/SKILL.md` — skill being tuned.
- `benchmarks/remote-host-diagnostics/` — public and holdout suites plus generated fixtures.
- `cmd/skillbench`, `cmd/skillfixtures`, `cmd/skilltrain` — Go CLIs. Run any command with `-h` for current flags and defaults.
- `runs/` — generated benchmark/training outputs, including per-iteration `SKILL.candidate.md`, `SKILL.previous.md`, and `SKILL.diff` artifacts from `skilltrain`.

## Usage

Run Go tooling from this `auto-improve-skills` directory; it is a standalone Go module. Ensure `pi` is installed/authenticated and the containing rshell checkout has `./rshell` built (`cd .. && make build` if needed).

Benchmark:

```sh
go run ./cmd/skillbench
```

Training loop:

```sh
go run ./cmd/skilltrain -iters 3 -judge
```

`skilltrain` writes per-iteration `sanitized-feedback.md` plus an auditable `sanitized-feedback.source.json`. By default feedback is generated deterministically from aggregate closed-tag counts and rendered as approved generic feedback cards. Granular card IDs roll up to parent tags for safe fallback; recurring granular cards suppress their broader parent cards. Pass `-feedback-llm` to let a nested no-tools Pi call choose/prioritize among those approved generic feedback cards only; raw benchmark prompts, outputs, criterion names, and task facts are not provided to that call.

Generate fixtures only:

```sh
go run ./cmd/skillfixtures
```

Local validation for this module only:

```sh
make fmt
make test
```

Do not run parent-repository validation commands for auto-improve-skills changes.

Generated fixtures and run outputs are intentionally not committed.

## LLM context isolation

The harness starts nested `pi` agents with `--no-context-files` so they do not load `AGENTS.md`/`CLAUDE.md` from this directory, parent directories, or the global Pi agent directory. Benchmark/researcher runs also disable discovered extensions, prompt templates, and skills except for the explicitly supplied benchmark skill.

If you start `pi` manually inside this nested directory and want the same behavior, use `pi --no-context-files` (or `pi -nc`).
