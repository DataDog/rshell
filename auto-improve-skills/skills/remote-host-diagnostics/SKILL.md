---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no Datadog remote-action tools, host reads, writes, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs use supplied roots only, never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Use long flag; comma roots. Inventory fallback too; if primary empty, say so.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route/true`. No whole-log `cat`, redirects, write/delete/edit, service/process/config changes.

## Workflow

1. Inventory once: `find <ROOT> -maxdepth 3 -type f | head -n 60`; use listed current/rotated files; no assumed globs.
2. Budget 4-5 calls incl. help+inventory; 6 if evidence missing. First grep likely files; include cross-source logs when requested/needed:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive symptom>)' <files> | head -n 40` (60 max). Filter `heartbeat|DEBUG` before `head` if noisy.
3. Then <=2 targeted cause/correlation/count/negative checks. Skip context if grep snippets suffice. Stop after finding plus requested scale/cross-source/negative/red herring.
4. Expected no-match: pipe to `head`/`; true`; don't retry.
5. Auth: count failed sources/users (`grep|sort|uniq -c`), check same-source `Accepted`, list other `Accepted`. If none, final exact phrase: `No accepted login from that source`; avoid `successful` near suspect actor/IP.
6. Flags/`ss`: `help <command>` first. For sockets run one `ss -tlnH`/`ss -tln`; no `-p` unless listed; state PID/process unavailable. Count/variants only if asked.
7. Failed command: inspect error/help; correct once.

## Final answer

Bullets: scope (`local fixture logs only` for fake fixtures; no real-host claim); finding/confidence; evidence (`<ROOT>/file:line` snippets/counts/times/IDs/actors); red herrings/negatives; `./rshell` commands (roots as `<ROOT>`/`<HOST_ROOT>`, no full repo paths); read-only next checks (`inspect/verify/count/list`).

Avoid in final incl. paths/negatives: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, or `config-change`.
