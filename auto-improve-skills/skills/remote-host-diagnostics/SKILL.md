---
name: datadog/remote-host-diagnostics
description: Use ./rshell for bounded, read-only remote diagnostics
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell` for remote-host commands and file inspection.

## Workflow

1. Define scope from the prompt: target host, symptom, and any provided paths, services, time ranges, or roots.
2. If syntax or available flags are uncertain, check `./rshell --help` or the remote command's help before assuming familiar options.
3. Run narrow, read-only probes first. Prefer targeted status checks, small log excerpts, and specific file reads over broad dumps, recursive searches, or unbounded log output.
4. Correlate evidence across relevant layers (for example service state, application logs, configuration, host resources, and network checks) and separate likely signal from unrelated noise.
5. Stop once the likely cause is supported, or when additional read-only checks would not materially reduce uncertainty.

## Safety and scope

- Stay within prompt-provided hosts, paths, and filesystem roots; inspect remote files only through `./rshell`.
- Do not make changes, restart services, delete files, install packages, or run destructive commands.
- Keep commands bounded with explicit files, filters, limits, or time windows when possible.

## Final answer

- List the important commands run and the observations they produced.
- State the likely cause with confidence, and call out missing or ambiguous evidence.
- Recommend only safe read-only follow-up checks; leave remediation to the operator.
