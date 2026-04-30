# Auto-Improve Skills

Autoresearch-style tooling for automatically improving Agent Skills with fixed benchmarks, nested `pi` runs, and git-tracked accepted iterations.

The current target is:

```text
auto-improve-skills/skills/remote-host-diagnostics/SKILL.md
```

The loop is inspired by <https://github.com/karpathy/autoresearch>: keep the benchmark fixed, let an LLM edit one target file, measure the candidate, then keep or reject it.

## Layout

```text
program.md                                      Instructions for researcher agents
skills/remote-host-diagnostics/SKILL.md         Target skill being improved
benchmarks/remote-host-diagnostics/cases.yaml   Benchmark cases and deterministic scoring criteria
benchmarks/remote-host-diagnostics/fixtures/    Fake logs used by benchmark investigations
cmd/skillbench/                                 Go benchmark runner
cmd/skilltrain/                                 Go improvement-loop orchestrator
internal/autoresearch/                          Shared Go types/helpers
runs/                                           Benchmark/training outputs, gitignored except .gitkeep
report/remote-host-diagnostics-autoresearch.html Single-file slide report
```

## Prerequisites

- Run from the rshell repository root.
- Ensure local `./rshell` exists. The benchmark runner can build it if missing, but explicit setup is:

```sh
make build
```

- `pi` must be available and authenticated for `openai-codex/gpt-5.5`.

## Run the benchmark

```sh
go run ./auto-improve-skills/cmd/skillbench \
  -model openai-codex/gpt-5.5
```

Useful variants:

```sh
# Quick smoke test
go run ./auto-improve-skills/cmd/skillbench -limit 1

# One specific case
go run ./auto-improve-skills/cmd/skillbench -case datadog-agent-config-regression

# More semantic, more expensive scoring with LLM-as-judge
go run ./auto-improve-skills/cmd/skillbench -judge
```

The runner writes a JSON report and raw nested-`pi` JSONL transcripts under `auto-improve-skills/runs/`.

## Run the training loop

Commit or stash unrelated changes first, then run:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -model openai-codex/gpt-5.5 \
  -iters 3 \
  -judge
```

The loop:

1. Runs a baseline benchmark.
2. Invokes `pi` as a researcher to edit only `SKILL.md`.
3. Runs the benchmark again.
4. Commits the skill edit if the normalized score improves by at least `-min-delta`.
5. Reverts the skill edit if it does not improve.

For a safe proof run that exercises the loop without committing:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -iters 1 \
  -limit 1 \
  -dry-run \
  -allow-dirty \
  -run-dir auto-improve-skills/runs/train-proof
```

## Current benchmark suite

The initial suite measures final-answer quality across realistic fake investigations:

- Datadog Agent config regression
- SSH brute-force summary
- Checkout HTTP 500/502 root-cause correlation
- Containerized Agent host-log fallback
- Unsupported `ss` flag recovery

More cases can be added to `benchmarks/remote-host-diagnostics/cases.yaml` without changing Go code.

## Report

Open the slide report in a browser:

```text
auto-improve-skills/report/remote-host-diagnostics-autoresearch.html
```
