---
name: datadog/remote-host-diagnostics
description: Diagnose remote-host-style incidents using local ./rshell with bounded, read-only evidence gathering.
---

# Remote Host Diagnostics

## Mandatory rshell usage
- Run diagnostics only through local `./rshell --allow-all-commands -c "<script>"`; do not inspect target logs with host tools and do not pipe scripts into rshell.
- First command: `./rshell --allow-all-commands --timeout 5s -c 'help'`. Then use `help <builtin>` before nontrivial flags or after an unsupported flag. Production rshell deployments may restrict, omit, or extend commands/features; builtin `help` in that environment is the source of truth.
- rshell is not full bash; prefer simple builtins such as `find`, `grep`, `head`, `tail`, `wc`, `sort`, `uniq`, `sed`, `ss`, `ps`, `ip`, and `ping`. Avoid arrays, arithmetic expansion, functions, `while`, `case`, background jobs, writes, and advanced expansions.
- For any file access, include `--allowed-paths <exact-root>` on the same rshell command. If the prompt gives multiple candidate roots (primary, mounted/fallback, etc.), include all relevant roots as comma-separated `--allowed-paths root1,root2` for every command that may touch them. Put prompt path strings directly in `--allowed-paths` so the audit trail shows the allowed roots.
- Keep commands read-only. Never edit, delete, restart, kill, or apply changes during diagnosis.

## Fast bounded workflow
1. Parse the prompt: symptom, time window, supplied roots, likely log families/components, and requested questions (cause, scale, success/failure, safe next check).
2. Inventory narrowly. Run a bounded inventory for each supplied root; if a primary root is empty, say so and inspect the supplied fallback/mounted root.
```sh
./rshell --allow-all-commands --timeout 10s --allowed-paths <root[,root2]> -c 'find <root> -maxdepth 3 -type f -print | head -n 100'
```
3. Search focused files, not whole trees. Use file lists from inventory, current logs first and rotated/adjacent logs only as needed. Bound every content command.
```sh
./rshell --allow-all-commands --timeout 15s --allowed-paths <root> -c 'grep -HnE -m 80 "<time-or-symptom-or-error-terms>" <file1> <file2>; grep -HnE -m 40 "<health-or-red-herring-terms>" <file1> <file2>'
```
Avoid unbounded dumps: no full-log `cat`, no recursive/broad content scans, and no `find ... -exec grep ...` over log trees. Use explicit file lists plus `tail -n N`, `head -n N`, `grep -m N`, `grep -c`, and `wc -l`.
4. Correlate and stop. Two to five well-chosen rshell commands are usually enough: help, inventory, focused evidence, optional counts/negative check. Do not repeat broad searches once evidence answers the prompt.
5. For socket/network questions, discover support first.
```sh
./rshell --allow-all-commands --timeout 5s -c 'help ss'
```
Use only flags shown by help. Do not assume process/PID output flags exist; if unavailable, state that only addresses/ports/states can be collected.

## Diagnostic patterns
- Regression/outage: compare first failure near the reported window with preceding reload/config/change messages and later stopped/healthy indicators. Distinguish separate healthy subsystems from the failing path.
- Security/auth summary: count repeated failures by source/user with bounded filters; separately search for accepted/success lines for the same source and for other sources before claiming compromise.
- Cross-component application errors: correlate edge symptoms with application/dependency/system evidence by time; identify the earliest/most causal dependency error, not merely the last noisy symptom line.
- Fallback/container layouts: explicitly report empty primary roots and evidence from host-mounted/fallback roots; every command touching either root must include all relevant roots in `--allowed-paths`.

## Final answer checklist
- State the likely cause, confidence, and uncertainty.
- Cite concrete evidence from command output: file/source name, relevant line snippets, counts, and timing/order. Do not claim a real remote host was accessed when using local fixtures.
- Separate root cause from red herrings with evidence.
- List the rshell commands you ran, including help and allowed paths.
- If suggesting next steps, make them safe read-only checks (inspect, verify, compare metrics/logs/config status). Avoid remediation commands unless the user explicitly requested a remediation plan, and never imply you ran remediation.
