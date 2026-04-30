# Auto-Improve Skills

Autoresearch-style loop for improving Agent Skills.

The first target is `skills/remote-host-diagnostics/SKILL.md`. The fixed benchmark suite lives under `benchmarks/remote-host-diagnostics/`; the Go runner invokes nested `pi` sessions that load the skill and perform fake local investigations through `./rshell` against fixture logs.

## Layout

```text
program.md                                      improvement instructions for researcher agents
skills/remote-host-diagnostics/SKILL.md         target skill
benchmarks/remote-host-diagnostics/cases.yaml   benchmark cases and scoring rubrics
benchmarks/remote-host-diagnostics/fixtures/    fake logs used by the cases
cmd/skillbench/                                 Go benchmark runner
cmd/skilltrain/                                 Go improvement loop orchestrator
runs/                                           benchmark/training outputs (gitignored except .gitkeep)
report/index.html                               slide report
```

## Run benchmarks

```sh
go run ./auto-improve-skills/cmd/skillbench
```

Useful flags:

```sh
# quick smoke test
go run ./auto-improve-skills/cmd/skillbench -limit 1

# one case
go run ./auto-improve-skills/cmd/skillbench -case agent-config-regression

# more semantic but more expensive scoring
go run ./auto-improve-skills/cmd/skillbench -judge
```

The runner writes a JSON report and raw nested-`pi` JSONL transcripts under `auto-improve-skills/runs/`.

## Run the training loop

Commit or stash unrelated changes first, then run:

```sh
go run ./auto-improve-skills/cmd/skilltrain -iters 3 -judge
```

The loop benchmarks the current skill, asks `pi --model openai-codex/gpt-5.5` to improve only `SKILL.md`, benchmarks the candidate, commits accepted improvements, and reverts rejected candidates.
