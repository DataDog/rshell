# Auto-Improve Program: remote-host-diagnostics

Improve the `remote-host-diagnostics` skill with a fixed benchmark harness. Keep changes small, general, and auditable.

## Edit scope

During skill tuning, edit only:

```text
auto-improve-skills/skills/remote-host-diagnostics/SKILL.md
```

Do not edit benchmark cases, holdout cases, generated fixtures/logs, Go tooling, reports, or run outputs unless explicitly asked for framework changes. Do not commit `auto-improve-skills/benchmarks/remote-host-diagnostics/generated-fixtures/`.

## Objective

Optimize in this order:

1. Final-answer quality: correct finding/root cause, grounded in command output, explicit about commands run, read-only/safe, clear about uncertainty and next steps.
2. Efficiency: gather enough evidence, then stop; avoid broad or repetitive searches.
3. Skill size: keep essential guidance, remove duplication and over-specific prose.

## Anti-overfitting

Treat benchmark data as samples, not targets. Do not encode case names, prompt wording, fixture-only paths or filenames, IPs, timestamps, IDs, log snippets, root causes, line numbers, or expected-answer templates. Add only general diagnostic behavior that should help unseen incidents.

## Required behavior

- Use only `./rshell` (Bash like)

## Benchmark commands

Run from the repository root.

Full suite:

```sh
go run ./auto-improve-skills/cmd/skillbench \
  -model openai-codex/gpt-5.5 \
  -cases auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml \
  -skill auto-improve-skills/skills/remote-host-diagnostics
```

Smoke test:

```sh
go run ./auto-improve-skills/cmd/skillbench -limit 1
```

Holdout gate:

```sh
go run ./auto-improve-skills/cmd/skillbench \
  -cases auto-improve-skills/benchmarks/remote-host-diagnostics/holdout.yaml
```

Cheap validation without nested agent runs:

```sh
go run ./auto-improve-skills/cmd/skillbench -mode prompts -ensure-rshell=false
```

Optional semantic judge:

```sh
go run ./auto-improve-skills/cmd/skillbench -judge
```

Training loop:

```sh
go run ./auto-improve-skills/cmd/skilltrain \
  -model openai-codex/gpt-5.5 \
  -iters 3 \
  -judge
```

## Improvement workflow

Inspect `auto-improve-skills/runs/.../result.json` and transcripts for recurring patterns, not exact facts. Prefer changes that fix general failures: missing direct finding, weak evidence, omitted commands, unsafe/broad searches, excessive follow-up, or duplicated guidance. The trainer accepts candidates only when the composite objective improves while quality remains within the allowed public and holdout tolerances.
