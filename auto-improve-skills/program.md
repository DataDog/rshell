# Auto-Improve Program: remote-host-diagnostics

Researcher-facing guidance for improving the `remote-host-diagnostics` skill. Keep changes small, general, and auditable.

## Edit scope

Edit only the skill file named in the researcher prompt. In the isolated workspace this is usually:

```text
skills/remote-host-diagnostics/SKILL.md
```

Do not edit evaluator artifacts, Go tooling, reports, run outputs, or unrelated files.

## Objective

Optimize in this order:

1. Final-answer quality: correct finding/root cause, grounded in command output, explicit about commands run, read-only/safe, clear about uncertainty and next steps.
2. Efficiency: gather enough evidence, then stop; avoid broad or repetitive searches.
3. Skill size: keep essential guidance, remove duplication and over-specific prose.

## Anti-overfitting

Use only this program, the current skill file, and any LLM-generated sanitized aggregate feedback included in the prompt. Do not inspect evaluator-private files or artifacts.

Treat sanitized feedback as generic process guidance only. It is generated from safe aggregate metrics, not raw task facts. Do not infer hidden task details from it.

Do not encode exact case facts, prompt wording, paths, filenames, IPs, timestamps, IDs, log snippets, root causes, line numbers, or expected-answer templates. Add only general diagnostic behavior that should help unseen incidents.

## Required behavior

- Use only `./rshell`.
- Keep diagnostics read-only and bounded.
- Prefer general workflow guidance over case-specific rules.

## Improvement workflow

Read the current skill, make the smallest useful general improvement, and summarize what changed plus why each material change improves quality, efficiency, or concision. If there is no clear safe improvement, leave the skill unchanged and say why.
