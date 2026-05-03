# Auto-Improve Skills

Harness for improving `skills/remote-host-diagnostics/SKILL.md` with fixed benchmarks, nested Codex runs, and git-tracked accepted iterations.

## Contents

- `program.md` — instructions given to researcher agents.
- `skills/remote-host-diagnostics/SKILL.md` — skill being tuned.
- `benchmarks/remote-host-diagnostics/` — public and holdout suites plus generated fixtures. Public cases use deterministic seeded variants so prompts/fixtures are not one fixed memorization target.
- `cmd/skillbench`, `cmd/skillfixtures`, `cmd/skilltrain` — Go CLIs. Run any command with `-h` for current flags and defaults.
- `runs/` — generated benchmark/training outputs.

## Usage

Run Go tooling from this `auto-improve-skills` directory; it is a standalone Go module. Ensure the Codex CLI is installed/authenticated and the containing rshell checkout has `./rshell` built (`cd .. && make build` if needed).

Benchmark:

```sh
go run ./cmd/skillbench
```

Training loop:

```sh
go run ./cmd/skilltrain -iters 3 -judge
```

`skilltrain` asks for a general skill improvement per iteration, benchmarks the candidate, and accepts it only when quality stays within tolerance and the objective improves. Case templates with `variants:` are expanded into concrete seeded benchmark cases before selection/scoring.

Each training iteration writes skill artifacts under `runs/`: `SKILL.previous.md`, `SKILL.candidate.md`, and `SKILL.md.diff`. Baseline directories include `SKILL.candidate.md` for the starting skill snapshot.

Researcher agents run from the repository root with Codex in workspace-write mode so they can inspect the public benchmark suite, inspect the best public `result.json`, and edit the skill. The prompt forbids reading, listing, grepping, or editing holdout-related files, folders, fixtures, run outputs, or results.

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

## LLM execution

The harness starts nested `codex exec` agents with `--ephemeral`, `--ignore-user-config`, `--ignore-rules`, `-c service_tier="fast"`, and `-c model_reasoning_effort="xhigh"`. Benchmark agents use `--json` so the harness can score final answers and command output from Codex JSONL events. The default model is `gpt-5.5`.

Benchmark agents run from a disposable temporary working directory containing only a link/copy of `./rshell`, so accidental relative-path writes from model-generated shell commands do not dirty the checkout. The benchmark skill text is embedded in the prompt.

If you start Codex manually inside this nested directory and want similar non-interactive behavior, use `codex exec --ephemeral --ignore-user-config --ignore-rules`.
