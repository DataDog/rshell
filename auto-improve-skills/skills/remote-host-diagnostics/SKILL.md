---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no Datadog remote-action/nonlocal tools, host reads, old docs, writes, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: use supplied roots; never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Long `--allowed-paths`; comma primary+fallback. If primary is empty, inspect fallback and say so.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route`. No whole-log `cat`, redirects, writes/deletes/edits, or service/process/config changes.

## Workflow

1. Inventory once: `find <ROOT> -maxdepth 3 -type f | head -n 60`; choose relevant current/rotated files.
2. Budget 4-6 `./rshell` calls incl. help+inventory; 7 only if evidence is missing. First grep: pair time with failure/symptom; avoid standalone broad terms:
   `grep -HnEi '(<time>.*(error|warn|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive symptom>|<component>.*(error|fail))' <files> | head -n 60`.
   If noise dominates, filter `heartbeat|DEBUG` before `head` unless it is the symptom. Then <=3 narrow confirm/correlate/count/negative checks; no duplicate broad greps.
3. Stop after the finding, needed cross-source evidence, targeted red-herring/negative, and requested scale/count; skip no-op checks.
4. Auth/security: count failed actors (`grep|sort|uniq -c|wc -l`), check Accepted/success for the same actor, and list Accepted logins from other sources. If none, say `No accepted login from that source`; avoid `successful` next to the suspicious actor.
5. `ss`/uncertain flags: run `help <command>` first; use `ss -tln`/`ss -tlnH`; no `-p` unless help lists it; state PID/process info is unavailable when unsupported.
6. Failed command: inspect error/help, correct once, do not guess.

## Final answer

Bullets: scope (`local fixture logs only` for fake fixtures; no real-host claim); finding/confidence; evidence (`file:line` snippets/counts with times/IDs/actors); red herrings/negatives; actual `./rshell` commands (long roots may be `<ROOT>` after first); read-only next checks (`inspect/verify/count/list`).

Never write `restart`, `kill/killed`, `delete`, `edit`, `apply` even in negatives; say `service lifecycle`, `process termination`, or `config-change`.
