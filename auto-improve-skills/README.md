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
benchmarks/remote-host-diagnostics/cases.yaml        Benchmark cases and deterministic scoring criteria
benchmarks/remote-host-diagnostics/generated-fixtures/ Generated fake logs (gitignored; recreated deterministically)
cmd/skillbench/                                      Go benchmark runner
cmd/skillfixtures/                                   Deterministic fixture generator
cmd/skilltrain/                                      Go improvement-loop orchestrator
internal/autoresearch/                          Shared Go types/helpers
runs/                                           Benchmark/training outputs, gitignored except .gitkeep
report/*.html                                  Single-file proof/report slides
```

## Prerequisites

- Run from the rshell repository root.
- Ensure local `./rshell` exists. The benchmark runner can build it if missing, but explicit setup is:

```sh
make build
```

- `pi` must be installed and authenticated for `openai-codex/gpt-5.5`.
  - The Go tools now auto-detect `pi` from `PATH`, `PI_BIN`, npm global prefix, and common nvm locations.
  - If auto-detection fails, pass `-pi /absolute/path/to/pi` or set `PI_BIN=/absolute/path/to/pi`.
  - Example nvm path on this machine: `/Users/alexandre.yang/.nvm/versions/node/v22.18.0/bin/pi`.

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

# Holdout acceptance suite used by skilltrain as a gate by default
go run ./auto-improve-skills/cmd/skillbench \
  -cases auto-improve-skills/benchmarks/remote-host-diagnostics/holdout.yaml

# More semantic, more expensive scoring with LLM-as-judge
go run ./auto-improve-skills/cmd/skillbench -judge
```

The runner deterministically regenerates large fake log fixtures under `auto-improve-skills/benchmarks/remote-host-diagnostics/generated-fixtures/` before each run. The generated logs are gitignored.

The runner writes a JSON report and raw nested-`pi` JSONL transcripts under `auto-improve-skills/runs/`. Cases run concurrently by default up to `-parallel-cases 3`; set `-parallel-cases 1` for serial execution or `0` for all selected cases. Reports include quality scores plus a soft composite objective (`objective_normalized_score`) that accounts for wall-clock duration and skill size. Deterministic scoring can require evidence in tool output for a final-answer claim, and hard safety gates zero a case if the transcript violates benchmark invariants such as direct fixture reads, write/remediation commands, missing `--allowed-paths` for fixture log reads, or whole-log dumps.

If you see `exec: "pi": executable file not found in $PATH`, either update to this version of the tooling or pass an explicit binary:

```sh
go run ./auto-improve-skills/cmd/skillbench \
  -pi /Users/alexandre.yang/.nvm/versions/node/v22.18.0/bin/pi
```

## Run the training loop

Commit or stash unrelated changes first, then run:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -model openai-codex/gpt-5.5 \
  -iters 3 \
  -judge
```

To repeat the entire training run multiple times in one `skilltrain` process, use `-loop-count`. The `trainloop` Make target is equivalent to 21 full runs of the supplied `-iters 3 -judge -model openai-codex/gpt-5.5` configuration:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -model openai-codex/gpt-5.5 \
  -iters 3 \
  -judge \
  -loop-count 21
```

When `-run-dir` is provided with `-loop-count N`, each full training run writes under `loop-001`, `loop-002`, and so on below that base directory.

The loop:

1. Runs baseline benchmarks. Public and holdout baselines run concurrently by default.
2. Invokes `pi` as a researcher to edit only `SKILL.md`.
3. Runs the candidate benchmark again, with a speculative holdout benchmark in parallel when holdout is enabled.
4. Averages each public benchmark result over `-repeats` runs (default 3) to reduce judge/runtime noise. Repeats run concurrently up to `-parallel-repeats` (default 3), and each nested `skillbench` runs cases concurrently up to `-parallel-cases` (default 3).
5. Uses the holdout suite from `-holdout-cases` (default `auto-improve-skills/benchmarks/remote-host-diagnostics/holdout.yaml`) as an acceptance gate for public-suite improvements; pass `-holdout-cases ""` to disable it.
6. Commits and pushes the skill edit if the composite objective improves by at least `-min-delta` (default 0.001), public quality stays within `-quality-tolerance`, and holdout quality stays within `-holdout-quality-tolerance` (defaults to `-quality-tolerance`); pass `-push=false` to keep accepted commits local.
7. Reverts the skill edit if it does not improve or fails the holdout gate.
8. If `-loop-count` is greater than 1, starts the next full run with the same flags and exits immediately on the first error, matching the old shell `trainloop` behavior.

Accepted commit subjects include the objective percentage change (`old% -> new%`). Commit bodies include the benchmark report path, quality/objective/duration/size scores, repeat counts, holdout gate summary when enabled, per-case scoring details, the researcher summary, and a diffstat. Accepted commits are pushed by default; pass `-push=false` to review and push them manually.

If `pi` is outside your shell `PATH`, use the same `-pi` flag:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -pi /Users/alexandre.yang/.nvm/versions/node/v22.18.0/bin/pi \
  -model openai-codex/gpt-5.5 \
  -iters 3 \
  -judge
```

For a safe proof run that exercises the loop without committing:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -iters 1 \
  -limit 1 \
  -repeats 1 \
  -holdout-cases "" \
  -dry-run \
  -allow-dirty \
  -run-dir auto-improve-skills/runs/train-proof
```

## Fixture generation

Generate or refresh the deterministic fixtures without running nested agents:

```sh
go run ./auto-improve-skills/cmd/skillfixtures
```

The generated files are intentionally not committed. They contain 500-2,000 lines per log file with rotations, red herrings, cross-service correlations, and container/host-mounted log layouts.

## Current benchmark suite

The suite measures final-answer quality across realistic fake investigations. It also records a simple efficiency objective: final-answer quality remains the primary score, with soft penalties for end-to-end wall-clock investigation duration and estimated `SKILL.md` token size.

- Datadog Agent config regression hidden among integration/APM/intake noise
- SSH brute-force summary with approximate counting and no-compromise distinction
- Checkout HTTP 500/502 root-cause correlation to PostgreSQL pool/slot exhaustion
- Containerized Agent host-log fallback with x509 failures caused by clock skew
- Unsupported `ss` flag recovery

A holdout acceptance suite lives in `benchmarks/remote-host-diagnostics/holdout.yaml`. It uses separate generated fixture facts, including adversarial cases for same-source accepted SSH logins, Datadog API-key failures that are not YAML line-42 regressions, and Redis/cache 503s with rotated DB-pool red herrings. `skilltrain` runs this suite as a gate by default.

More cases can be added to `benchmarks/remote-host-diagnostics/cases.yaml` or the holdout suite without changing Go code.

## Report

Open the slide reports in a browser:

```text
auto-improve-skills/report/remote-host-diagnostics-autoresearch.html
auto-improve-skills/report/skilltrain-loop-count.html
```
