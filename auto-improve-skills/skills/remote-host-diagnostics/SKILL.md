---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell` for remote inspection. Keep all actions read-only, bounded, and limited to prompt-provided hosts/paths; inspect files only through `./rshell`.

## Workflow

1. Check `./rshell --help` and, when needed, remote command help before assuming flags or syntax; if a flag is rejected, adapt to supported options instead of retrying blindly.
2. Orient quickly: confirm the target/context, current directory, relevant services/processes, disk/network basics, and recent logs/configs tied to the symptom.
3. Prefer targeted commands (`pwd`, `ls`, `cat`/`head`/`tail`, `grep`, `ps`, `ss`, `df`, service/log status commands) over broad scans. Avoid writes, restarts, package changes, credential access, and large recursive searches.
4. Gather enough corroborating evidence, then stop; do not repeat equivalent checks.

## Final answer

State the commands run, key output evidence, likely finding/root cause, confidence, and any missing evidence or safe next checks if output is incomplete or ambiguous.
