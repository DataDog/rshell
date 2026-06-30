# Command Candidates

## Decision Lens

LLM-based agents are the main users of rshell. Candidate decisions should optimize rshell's usefulness for AI agents resolving investigations: commands should be easy for agents to choose correctly, produce bounded and explainable output, expose enough host context to diagnose issues, and keep remediation actions explicit, auditable, and constrained by rshell's safety model.

## Entry Format

This is a concise decision record for generally useful investigation commands. Each entry should explain whether the command is a good fit for rshell and why. Use 🟢 for reasons to support it and 🔴 for reasons to reject, defer, or narrow the scope, so the decision is easy to scan.

Each entry should include: type, decision, evidence, existing coverage, minimum subset, target syntax, fit/scope rationale, and implementation boundaries. Put accepted or planned work under "Accepted Candidates" and rejected or deferred work under "Rejected / Deferred Candidates". Rejected alternatives should usually live inside the related candidate entry; add standalone rejected entries only when the command is likely to be proposed again.

## Accepted Candidates

### `stat`

Type: new builtin

Decision: add narrow builtin

Evidence: the inode exhaustion runbook uses `stat -f /var/spool/` to confirm total and free inodes for the filesystem backing a specific path.

Already covered? Partially covered by `df -i` / `df -ih`, but `df` currently does not accept `FILE` operands. There is no direct way to ask "which filesystem backs this path, and how many inodes are free there?"

Minimum subset: `stat -f PATH...`

Target syntax:
- `stat -f /var/spool/`

🟢 Fit: useful for path-targeted filesystem inode investigation.

🔴 Scope: do not start with a full GNU/BSD `stat` implementation.

Implementation boundary: user-supplied paths must go through `AllowedPaths`; unlike `df` mount enumeration, these paths are operator input rather than hardcoded kernel pseudo-files.

### `ps` memory fields and sorting

Type: existing builtin enhancement

Decision: add narrow enhancement

Evidence: common host-pressure investigation needs deterministic process memory sorting for agents.

Already covered? Partially covered by `ps`, but current `ps` omits RSS, VSZ, `%MEM`, custom columns, and sorting.

Minimum subset:
- `ps -e -o pid,ppid,comm,rss,vsz,pmem --sort=-rss`
- `ps -e -o pid,ppid,comm,rss,vsz,pmem --sort=-pmem`

Target syntax:
- `ps -e -o pid,ppid,comm,rss,vsz,pmem --sort=-rss | head`
- `ps -e -o pid,ppid,comm,rss,vsz,pmem --sort=-pmem | head`

🟢 Fit: single-shot, bounded output is better for LLM agents than live terminal UI output.

🟢 Fit: builds on the existing `ps` investigation builtin and follows familiar `ps -o` / GNU `--sort` syntax.

🔴 Scope: do not expose full argv fields such as `args` or `command`; preserve the current process-name-only privacy boundary.

Implementation boundary: keep supported `-o` fields explicit and small. Prefer piping to `head` for top-N output instead of inventing a non-standard `--limit` flag.

## Rejected / Deferred Candidates

### `top`

Type: new builtin

Decision: do not add initially

Evidence: process pressure investigations often ask for "top processes", but agents need deterministic, bounded output that works in non-interactive scripts.

Already covered? Partially covered by `ps`. The accepted `ps` memory fields and sorting enhancement covers the agent-friendly workflow without introducing live terminal UI behavior.

Minimum subset: none initially.

Target syntax:
- Rejected: `top`
- Preferred alternative: `ps -e -o pid,ppid,comm,rss,vsz,pmem --sort=-rss | head`

🟢 Fit: familiar command name for CPU and memory pressure investigation.

🔴 Agent fit: interactive/live terminal output is harder for LLM agents to consume reliably than single-shot, bounded tables.

🔴 Scope: adding enough `top` compatibility to match user expectations would overlap heavily with `ps`, require terminal-oriented behavior, and increase maintenance cost without improving the core agent workflow.

Implementation boundary: defer `top` unless users specifically need its syntax. Prefer deterministic `ps` sorting for top-N process investigations.

### `systemd-tmpfiles`

Type: new builtin / remediation command candidate

Decision: do not add raw builtin; defer any deletion-oriented cleanup primitive

Evidence: the temp-file and build-artifact disk-space runbook lists `systemd-tmpfiles --clean` as remediation, plus a permanent-fix path that writes `/etc/tmpfiles.d/tmp-cleanup.conf` and then applies it with `systemd-tmpfiles --clean`.

Already covered? Investigation is mostly covered by `df`, `du`, `find`, `ls`, `sort`, and `head`. Remediation is intentionally only partially covered: `truncate` and `logrotate` recover space from explicit file operands through `AllowedPaths` `:rw`, while recursive deletion is not exposed and `find -delete` is blocked for sandbox safety.

Minimum subset: none for a raw `systemd-tmpfiles` builtin.

Target syntax:
- Rejected: `systemd-tmpfiles --clean`
- Rejected: `systemd-tmpfiles --dry-run --clean`

🟢 Fit: this is a familiar Linux remediation command and matches operator runbooks for scheduled `/tmp` cleanup.

🔴 Agent fit: the command text does not reveal the cleanup plan. With no explicit config operand, behavior is driven by host tmpfiles configuration, so an LLM agent cannot infer the deletion set from the script it produced.

🔴 Scope: `systemd-tmpfiles` is a broad system-policy engine, not a narrow temp cleanup command. It can create, remove, clean, write, adjust modes/ownership, use globs, and apply age rules from config. Reimplementing enough `tmpfiles.d` semantics to be compatible would be large and high-risk; wrapping the host binary would bypass rshell's builtin safety model.

🔴 Platform: Linux/systemd-only. This does not match rshell's preference for portable builtins unless a platform-specific command has unusually strong investigation value.

Implementation boundary: do not invoke the host `systemd-tmpfiles` binary from a builtin, do not parse tmpfiles.d config, and do not add recursive deletion as part of this candidate. If rshell later supports deletion for remediation, prefer a separate rshell-native cleanup helper with explicit path operands, explicit age/size predicates, dry-run output, context cancellation, traversal limits, and `AllowedPaths` `:rw` enforcement.
