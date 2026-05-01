# Auto-Improve Program: remote-host-diagnostics

This directory follows the spirit of Karpathy's `autoresearch`: keep the evaluation harness fixed, let an AI agent edit one target file, run a bounded benchmark, keep improvements, and iterate.

## Scope and allowed edits

During normal improvement iterations, only edit:

```text
auto-improve-skills/skills/remote-host-diagnostics/SKILL.md
```

Do not edit benchmark cases, fixture generation, Go tooling, reports, run outputs, or generated logs unless a human explicitly asks for framework changes. In particular:

- Do not edit `auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml` during skill tuning.
- Do not edit `auto-improve-skills/internal/autoresearch/fixtures.go` during skill tuning.
- Do not commit `auto-improve-skills/benchmarks/remote-host-diagnostics/generated-fixtures/`; it is generated and gitignored.
- Do not overfit the skill to benchmark cases. In particular, do not hard-code case names, prompt wording, fixture facts, specific IPs, transaction IDs, line numbers, root causes, filenames, or expected-answer templates. Improve general diagnostic behavior instead.

## Objective

Improve final-answer quality for diagnostics performed through the local `./rshell` binary. Quality is the primary goal, with soft secondary pressure for faster investigations and a smaller skill file. The skill should help an agent produce answers that are:

- correct about the likely root cause or finding
- grounded in command output/log evidence
- explicit about commands run
- safe and read-only
- clear about uncertainty and next steps
- efficient: stop once the finding is well supported instead of running broad or repetitive follow-up commands
- compact: keep safety-critical instructions, but avoid duplicated or over-specific guidance

## Anti-overfitting requirement

Treat benchmark cases as representative samples, not targets to memorize. Skill changes must improve general diagnostic behavior for unseen incidents, not encode benchmark-specific facts or expected answers.

Do not add guidance that depends on case names, exact prompt wording, fixture-specific filenames, IPs, timestamps, transaction IDs, log snippets, root causes, or answer templates. If a change only helps because it recognizes a benchmark case, prompt pattern, or generated fixture detail, reject it.

Prefer general investigation strategies: bounded searches, evidence gathering, cross-log correlation, uncertainty handling, and clear final answers grounded in observed command output.

## Invariants

- Use local `./rshell` through the Bash tool.
- Do not use Datadog remote-action tools.
- Keep diagnostics read-only.
- Prefer bounded log reads (`tail`, `head`, filtered `grep`, `wc`, `sort`, `uniq`, `find`) over reading entire logs.
- If the user gives a fake or explicit log root, use that root instead of hard-coded `/var/log`.
- For containerized layouts, handle empty primary log roots and inspect a provided host-mounted log root when available.
- Check command help before using flags that may be unsupported in this rshell build, especially `ss` process/PID flags.
- If a command fails, explain why and choose a corrected command only after inspecting the failure or help output.
- The benchmark measures final-answer quality first. It also considers investigation time and skill size as soft preferences.

## Generated fixtures

Benchmark logs are generated deterministically, not committed as static large files.

- `cmd/skillbench` regenerates fixtures automatically before running the remote-host-diagnostics suite.
- To regenerate them manually without nested agent runs:

  ```sh
  go run ./auto-improve-skills/cmd/skillfixtures
  ```

- Generated logs live under:

  ```text
  auto-improve-skills/benchmarks/remote-host-diagnostics/generated-fixtures/
  ```

- Fixture variables used by cases point at generated paths:
  - `{{LOG_ROOT}}`
  - `{{EMPTY_LOG_ROOT}}`
  - `{{HOST_LOG_ROOT}}`

The generated logs are intentionally noisy and larger: rotated files, red herrings, cross-service correlations, SSH/auth noise, Datadog Agent logs, nginx/app/system logs, and container host-log fallback layouts. Skill improvements should teach bounded investigation strategies that work on these patterns without memorizing fixture content.

## Benchmark

Run commands from the repository root.

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

For one failing case:

```sh
go run ./auto-improve-skills/cmd/skillbench -case datadog-agent-config-regression
```

To validate suite loading and fixture generation cheaply without nested live agent runs:

```sh
go run ./auto-improve-skills/cmd/skillbench -mode prompts -ensure-rshell=false
```

For a more semantic but more expensive score, enable the LLM judge:

```sh
go run ./auto-improve-skills/cmd/skillbench -judge
```

The JSON report includes the primary quality score plus a simple overall objective that softly rewards faster investigations and a smaller skill file.

## Scoring and acceptance design

Keep this design simple and auditable:

- Quality comes first. A faster or smaller skill is not useful if it gives worse diagnostic answers.
- Efficiency matters once quality is preserved. Prefer skill guidance that leads agents to gather enough evidence, then stop.
- Size matters as a soft preference. Keep important safety and workflow rules, but remove duplication and overly specific prose.
- Training accepts a candidate only when the overall objective improves and answer quality stays within the allowed tolerance of the best quality seen.

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
4. Commits the skill edit if the overall objective improves without dropping quality beyond the allowed tolerance; pass `-push` to push accepted commits automatically.
5. Reverts the skill edit if it does not improve.

## Improvement strategy for agents

When improving the skill, inspect failures in `auto-improve-skills/runs/.../result.json` and raw transcripts. First look for answer-quality misses:

- Did the final answer state the direct finding/root cause?
- Did it cite concrete evidence with filenames and relevant log snippets?
- Did it list the commands run?
- Did it separate likely cause from red herrings and old rotated-log events?
- Did it expose or dump unrelated log content instead of summarizing?
- Did it ignore a user-provided log root?
- Did it fail to search across correlated logs when the case requires cross-log evidence?
- Did it use unsupported flags like `ss -tlnp` instead of checking `help ss` or using `ss -tln`?
- Did it fail to handle containerized `/host/var/log` fallback?
- Did it propose write/remediation commands instead of safe read-only next checks?

Then look for objective misses:

- Did the agent spend many extra commands after enough evidence was found?
- Did it run broad searches before focused searches suggested by the prompt/time window?
- Did it check command help redundantly after support was already known for simple flags?
- Did the skill repeat guidance that could be merged or shortened?
- Did case-specific instructions grow when a shorter general diagnostic pattern would work?

Make small, general instruction changes that help future cases, rather than memorizing fixture content. Prefer deleting duplication or tightening workflow instructions over adding more case-specific prose.
