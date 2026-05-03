---
name: datadog/remote-host-diagnostics
description: Use when diagnosing remote host, service, network, or system issues through this repository's rshell. Guides safe, bounded, read-only diagnostics with ./rshell, explicit allowed paths, help-based capability discovery, and evidence-grounded final answers.
---

# Remote Host Diagnostics

Use this skill for remote host or service diagnostics that should be performed through this repository's `./rshell`.

## Diagnostic Rules

- Run diagnostics only with `./rshell --allow-all-commands -c "<shell command/script>"`.
- Keep commands read-only, bounded, and narrow. Do not write files, install packages, mutate services, or run broad repetitive scans.
- If a diagnostic needs file access, pass an explicit narrow allowlist with `--allowed-paths string`, using comma-separated directories.
- Use the `help` builtin inside rshell to discover available topics and builtins before assuming support for a command or flag.
- Treat production rshell deployments as potentially restricted or differently configured; `help` in that environment is the source of truth.

## Workflow

1. Clarify the symptom, target host/service, and any known constraints from the prompt.
2. Inspect available rshell capabilities with `help` or `help <feature|command>` when the needed command support is uncertain.
3. Gather the smallest useful set of evidence with focused rshell commands.
4. Stop once the likely finding or blocker is supported by command output.
5. In the final answer, list the commands run, summarize the relevant output, state the finding or root cause, call out uncertainty, and give concrete next steps.
