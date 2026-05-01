---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`. No Datadog remote-action/nonlocal tools, host-side reads, old skill docs, writes, or remediation.

## Commands

- Start: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Use supplied roots; never assume `/var/log`. For each log-read:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Use long `--allowed-paths`, never `-p`; primary+fallback is comma-separated. If primary is empty, inspect fallback too and say so.
- Builtins: `help`, `find`, `ls`, `grep`, `tail`, `head`, `wc`, `sort`, `uniq`, `cut`, `sed`, `strings`, `ss`, `ps`, `ip route`. No whole-log `cat`, redirects, writes, deletes, edits, service/process/config changes.

## Workflow

1. Inventory once: `find <ROOT> -maxdepth 3 -type f | head -n 80`; choose relevant files/log groups, not everything.
2. Budget 4-7 commands. Run one focused broad `grep -HnEi 'time|symptom|component|error' <files> | head -n 100`, then 1-3 confirm/count/negative greps. No duplicate broad greps; short `sed` context only if needed.
3. Stop when supported: primary failure, corroborating source for cross-layer incidents, targeted red-herring/negative, and requested scale/count. Skip extra counts unless they change the answer.
4. Auth/security: count failed actors (`grep|sort|uniq -c|wc -l`), check Accepted/success for the same actor, and list Accepted logins from other sources. If none: `No accepted login from that source`; avoid `successful` next to the suspicious actor.
5. `ss`/uncertain flags: run `help <command>` first; use `ss -tln`/`ss -tlnH`; no `-p` unless help lists it; state PID/process info is unavailable when unsupported.
6. Failed command: inspect error/help, correct once, do not guess.

## Final answer

Concise bullets:
- scope (`local fixture logs only` for fake fixtures; no real-host claim);
- finding/root cause plus confidence/uncertainty;
- evidence with filenames and short snippets/counts; preserve decisive timestamps, line numbers, IDs, actors, counts;
- red herrings/negative checks that mattered;
- actual `./rshell` commands run;
- read-only next checks only (`inspect`, `verify`, `count`, `list`). In next steps and negatives, avoid remediation-action words; prefer neutral phrases like `no service lifecycle/process termination/config-change evidence`.
