---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no Datadog remote-action/nonlocal tools, host reads, writes, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: supplied roots only; never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Long `--allowed-paths`; comma primary+fallback. If primary empty, inspect fallback and say so.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route`. No whole-log `cat`, redirects, writes/deletes/edits, service/process/config changes.

## Workflow

1. Inventory once: `find <ROOT> -maxdepth 3 -type f | head -n 60`; use relevant current/rotated files; no assumed globs.
2. Budget 4-6 `./rshell` calls incl. help+inventory; 7 only if evidence missing. First grep pairs time with symptom/severity, not generic components:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive symptom>)' <files> | head -n 60`.
   If noisy, put `grep -viE 'heartbeat|DEBUG'` before `head` unless it is the symptom.
3. Then <=3 narrow confirm/correlate/count/negative checks; stop after root-cause, needed cross-source evidence, red-herring/negative, and requested scale/count. No duplicate broad/no-op checks.
4. Auth/security: count failed actors (`grep|sort|uniq -c|wc -l`), check Accepted for same actor, and list Accepted from other sources. If none, say `No accepted login from that source`; avoid `successful` next to suspicious actor.
5. `ss`/uncertain flags: run `help <command>`; use `ss -tln`/`ss -tlnH`; no `-p` unless listed; state PID/process unavailable when unsupported.
6. Failed command: inspect error/help, correct once, do not guess.

## Final answer

Bullets: scope (`local fixture logs only` for fake fixtures; no real-host claim); finding/confidence; evidence (`<ROOT>/file:line` or `file:line` snippets/counts with times/IDs/actors); red herrings/negatives; `./rshell` commands (roots as `<ROOT>`/`<HOST_ROOT>`, never full repo/generated paths); read-only next checks (`inspect/verify/count/list`).

Avoid these words/substrings in final incl. paths/negatives: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, or `config-change`.
