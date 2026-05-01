---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only to run local `./rshell`. No Datadog remote-action/nonlocal tools, host-side log inspection, old skill docs, or writes.

## Command rules

- Start every session: `./rshell --allow-all-commands --timeout 5s -c 'help'`
- For supplied log roots, use the literal long flag on every touching command:
  `./rshell --allow-all-commands --allowed-paths <LOG_ROOT> --timeout 10s -c '<read-only command>'`
  Primary+fallback: `--allowed-paths <PRIMARY>,<HOST_ROOT>`. Never use `-p` for allowed paths.
- Read-only builtins only: `help`, `find`, `ls`, `grep`, `tail`, `head`, `wc`, `sort`, `uniq`, `cut`, `sed`, `strings`, `ss`, `ps`, `ip route`. No redirects, writes, edits, deletes, service/process/config changes, or whole-log `cat`.

## Fast workflow

1. Use explicit root(s); do not assume `/var/log`. If primary is empty and a host/fallback root is given, inspect both and mention it.
2. One narrow inventory: `find <ROOT> -maxdepth 3 -type f | head -n 80`.
3. Keep a small budget: after inventory, run 2-4 bundled searches/counts, e.g. `grep -HnEi 'symptom|time|component|error' <files> | head -n 100`. Combine patterns/files; avoid duplicate broad greps and use rotations only for timeline/red-herring checks.
4. Cross-layer incidents need enough evidence: primary failure log + one corroborating source + one targeted negative/red-herring check, then stop.
5. Auth/security: count actors (`grep`/`sort`/`uniq -c`/`wc -l`), check Accepted/success for the same actor, and list Accepted publickey/logins from other sources. If none, write: `No accepted login from that source`; avoid `successful` next to the suspicious IP/actor.
6. For `ss` or uncertain flags, run `help <command>` first. Use `ss -tln`/`ss -tlnH`; do not use process flags unless help lists them, and say PID/process data is unavailable when unsupported.
7. If a command fails, read the error/help and correct once; do not guess. Stop once supported.

## Final answer checklist

Use concise bullets with:
- finding/root cause plus confidence/uncertainty;
- evidence: filenames and short snippets/counts;
- red herrings/negative checks that affect the conclusion;
- exact `./rshell` commands run;
- only read-only next checks (`inspect`, `verify`, `count`, `list`); avoid write-action wording even in negatives.

For fake/local fixtures, say `local fixture logs only`; avoid saying a real host was contacted.
