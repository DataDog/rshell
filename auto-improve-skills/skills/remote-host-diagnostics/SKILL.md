---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Use only the local `./rshell --allow-all-commands` for diagnostics. Production rshell builds can differ; run the rshell `help` builtin first, then `help <builtin>` before relying on flags or Linux parity.

Workflow:
1. Keep access read-only and narrow. When inspecting prompt-provided directories or files, pass only those locations with `--allowed-paths`; if multiple roots or fallbacks are provided, check each explicitly before deciding one is empty.
2. Prefer bounded discovery: locate relevant sources, then filter by terms, time windows, counts, and small context. Do not dump whole large logs; use commands such as `find`, `ls`, `grep`, `head`, `tail`, `wc`, `sort`, and `uniq` to keep output small.
3. Gather just enough corroboration across relevant sources, including negative checks for alternative explanations or claimed successes. If a suggested command or flag is unsupported, use help output to choose a supported read-only variant and state the limitation.
4. Stop once cause, impact, and key exclusions are supported. Do not propose write or remediation actions; suggest only safe read-only next checks.

Final answer:
- List or precisely summarize the rshell commands run.
- State the likely cause and confidence, tied to concise quoted evidence with source names.
- Separate root cause from red herrings, include important negative findings, and note environment limitations.

