---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use only the Bash tool to run the local `./rshell` binary. Do not inspect log roots with host tools, search for old skill docs, modify files, or call any nonlocal execution tool.

## Required command pattern

- First command in each session:
  `./rshell --allow-all-commands --timeout 5s -c 'help'`
- For any user-provided log root, every `./rshell` command that touches it must use the long flag with the literal path:
  `./rshell --allow-all-commands --allowed-paths <LOG_ROOT> --timeout 10s -c '<read-only command>'`
  Use comma-separated literal roots for fallbacks: `--allowed-paths <PRIMARY>,<HOST_ROOT>`. Do not use `-p`; use `--allowed-paths`.
- Keep commands read-only: `help`, `find`, `ls`, `grep`, `tail`, `head`, `wc`, `sort`, `uniq`, `cut`, `sed`, `strings`, `ss`, `ps`, `ip route show/get` as needed. Never redirect/write, edit, delete, restart, kill, apply config, or dump whole logs with `cat`.

## Fast workflow

1. Use the explicit log root(s) from the prompt; do not assume `/var/log`. If the primary root is empty and a host-mounted/fallback root is supplied, inspect both and mention the fallback.
2. Inventory narrowly, e.g. `find <ROOT> -maxdepth 3 -type f | head -n 80`.
3. Search with focused, bounded filters from the symptom/time window and likely components, always capping broad output (`grep -HnEi 'pattern' ... | head -n 100`). Use rotated logs only to confirm old/red-herring events.
4. Correlate at least two relevant sources when the incident spans layers (app/proxy/system/agent/auth). For counts or security questions, use `grep` + `wc -l`/`sort`/`uniq`, and separately check whether the same actor also has success/accepted events.
5. For `ss` or uncertain flags, run `help <command>` first. Prefer `ss -tln` or `ss -tlnH`; do not use `-p`/process flags unless help explicitly lists them, and state PID/process data is unavailable when unsupported.
6. If a command fails, inspect the error/help and correct it once; do not repeat guesses.
7. Stop once the finding is supported by concrete evidence; avoid repetitive broad searches.

## Final answer checklist

Be concise and include:
- likely finding/root cause and confidence/uncertainty;
- evidence with filenames and short relevant snippets/counts;
- red herrings or negative checks that affect the conclusion;
- exact `./rshell` commands run;
- only safe read-only next diagnostic checks (not remediation commands). In fake/local fixture investigations, do not claim a real remote host was contacted.
