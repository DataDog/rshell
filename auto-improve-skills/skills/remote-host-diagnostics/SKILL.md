---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no Datadog remote-action/nonlocal tools, host-side reads, old skill docs, writes, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Use supplied roots; never assume `/var/log`. Log reads:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Use long `--allowed-paths` only; comma-separate primary+fallback. If primary is empty, inspect fallback too and say so.
- Read-only builtins: `help`, `find`, `ls`, `grep`, `tail`, `head`, `wc`, `sort`, `uniq`, `cut`, `sed`, `strings`, `ss`, `ps`, `ip route`. No whole-log `cat`, redirects, writes/deletes/edits, or service/process/config changes.

## Workflow

1. Inventory once: `find <ROOT> -maxdepth 3 -type f | head -n 80`; pick the small relevant file set.
2. Total budget: 5-7 `./rshell` calls including help+inventory. Do one focused grep (`grep -HnEi '<time/window>|<symptom>|<component>|error' <files> | head -n 80`), then <=3 confirm/correlate/count/negative checks. Use exact time fragments or time+symptom; avoid broad clock regexes/heartbeat dumps. No duplicate broad greps.
3. Stop once you have the primary failure, needed cross-source evidence, targeted red-herring/negative, and requested scale/count. Skip checks that won't change the answer.
4. Auth/security: count failed actors (`grep|sort|uniq -c|wc -l`), check Accepted/success for the same actor, and list Accepted logins from other sources. If none, say `No accepted login from that source`; avoid `successful` next to the suspicious actor.
5. `ss`/uncertain flags: run `help <command>` first; use `ss -tln`/`ss -tlnH`; no `-p` unless help lists it; state PID/process info is unavailable when unsupported.
6. Failed command: inspect error/help, correct once, do not guess.

## Final answer

Bullets: scope (`local fixture logs only` for fake fixtures; no real-host claim); finding/root cause with confidence; evidence with filenames + snippets/counts, keeping decisive timestamps/lines/IDs/actors; relevant red herrings/negatives; actual `./rshell` commands; read-only next checks (`inspect`, `verify`, `count`, `list`).

Final: never use `restart`, `kill/killed`, `delete`, `edit`, `apply` even in negatives; say `service lifecycle`, `process termination`, or `config-change` instead.
