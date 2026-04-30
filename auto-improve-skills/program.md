# Auto-Improve Program: remote-host-diagnostics

This directory follows the spirit of Karpathy's `autoresearch`: keep the evaluation harness fixed, let an AI agent edit one target file, run a bounded benchmark, keep improvements, and iterate.

## Target file

Only edit:

```text
auto-improve-skills/skills/remote-host-diagnostics/SKILL.md
```

Do not edit benchmark cases, fixtures, Go tooling, or reports during an improvement iteration unless a human explicitly asks for framework changes.

## Objective

Improve final-answer quality for diagnostics performed through the local `./rshell` binary. The skill should help an agent produce answers that are:

- correct about the likely root cause or finding
- grounded in command output/log evidence
- explicit about commands run
- safe and read-only
- clear about uncertainty and next steps

## Invariants

- Use local `./rshell` through the Bash tool.
- Do not use Datadog remote-action tools.
- Keep diagnostics read-only.
- Prefer bounded log reads (`tail`, `head`, filtered `grep`, `wc`, `sort`, `uniq`) over reading entire logs.
- If the user gives a fake or explicit log root, use that root instead of hard-coded `/var/log`.
- If a command fails, explain why and choose a corrected command only after inspecting the failure or help output.
- The benchmark measures final answer quality, not just command compliance.

## Benchmark

Run the fixed benchmark suite with:

```sh
go run ./auto-improve-skills/cmd/skillbench \
  -model openai-codex/gpt-5.5 \
  -cases auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml \
  -skill auto-improve-skills/skills/remote-host-diagnostics
```

For a quicker smoke test:

```sh
go run ./auto-improve-skills/cmd/skillbench -limit 1
```

For a more semantic but more expensive score, enable the LLM judge:

```sh
go run ./auto-improve-skills/cmd/skillbench -judge
```

## Training loop

After committing the benchmark framework, run:

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

## Improvement strategy for agents

When improving the skill, inspect failures in `auto-improve-skills/runs/.../result.json` and raw transcripts. Look for answer-quality misses:

- Did the answer omit the direct finding?
- Did it fail to cite evidence?
- Did it expose sensitive unrelated log lines?
- Did it ignore a user-provided log root?
- Did it use unsupported flags like `ss -tlnp` instead of checking `help ss` or using `ss -tln`?
- Did it fail to handle containerized `/host/var/log` fallback?

Make small, general instruction changes that help future cases, rather than memorizing fixture content.
