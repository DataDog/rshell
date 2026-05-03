# Auto-Improve Program: remote-host-diagnostics

rshell is a minimal, safety-oriented bash/POSIX-like shell interpreter for AI agents.

Researcher-facing guidance for improving the `remote-host-diagnostics` skill. Keep changes general and auditable.

## Edit scope

Edit only the skill file named in the researcher prompt. In this repository this is usually:

```text
auto-improve-skills/skills/remote-host-diagnostics/SKILL.md
```

Do not edit evaluator artifacts, Go tooling, reports, run outputs, or unrelated files. Do not read, inspect, list, grep, or edit holdout-related files/folders/results, including `holdout.yaml`, `generated-fixtures/holdout`, `iter-000-holdout`, or any path segment named `holdout`.

## Objective

Optimize in this order:

1. Final-answer quality: correct finding/root cause, grounded in command output, explicit about commands run, read-only/safe, clear about uncertainty and next steps.
2. Efficiency: gather enough evidence, then stop; avoid broad or repetitive searches.
3. Maintainability: remove duplication and over-specific prose.

## Anti-overfitting

Use only this program, the current skill file, the public benchmark suite, and the best public benchmark result named in the researcher prompt. Do not inspect evaluator-private or holdout files or artifacts.

Treat public benchmark data as samples, not targets. Do not infer hidden holdout task details.

Do not encode exact case facts, prompt wording, paths, filenames, IPs, timestamps, IDs, log snippets, root causes, line numbers, or expected-answer templates. Add only general diagnostic behavior that should help unseen incidents.

## Required behavior

- Use only `./rshell --allow-all-commands` for diagnostics in this repository.
- If diagnostics need file access, keep it explicit and narrow with `--allowed-paths string` (a comma-separated list of allowed directories).
- Use the `help` builtin inside rshell to discover available capabilities. `help` lists supported feature topics and builtins; `help <feature|command>` shows details for a specific topic, command, or flags.
- Keep diagnostics read-only and bounded.
- Prefer general workflow guidance over case-specific rules.
- Mention that production rshell deployments may restrict or omit some commands/features; `help` in that environment is the source of truth for what is supported.

## Improvement workflow

Read the current skill, make a useful general improvement, and summarize what changed plus why each material change improves quality or efficiency. Avoid benchmark-specific facts. If there is no clear safe improvement, leave the skill unchanged and say why.
