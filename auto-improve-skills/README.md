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

`skilltrain` normally asks for focused edits, but it also injects exploration to avoid purely local line-level search: every `-structural-interval` iterations (default 3) it permits larger structural mutations, and every `-rewrite-interval` iterations (default 5) it requests full rewrites from scratch. Those exploration iterations generate `-exploration-candidates` candidates (default 3), benchmark each on the public suite, then continue acceptance gating with the best candidate.

Researcher agents run from the repository root with read, bash, edit, and write tools so they can inspect the public benchmark suite and the best public `result.json`. The prompt forbids reading, listing, grepping, or editing holdout-related files, folders, fixtures, run outputs, or results.

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

Benchmark agents run from a disposable temporary working directory containing only a link/copy of `./rshell`, so accidental relative-path writes from model-generated shell commands do not dirty the checkout.

If you start `pi` manually inside this nested directory and want the same behavior, use `pi --no-context-files` (or `pi -nc`).
