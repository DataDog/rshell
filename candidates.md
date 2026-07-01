# Command Candidates

## Table Of Contents

- [Decision Lens](#decision-lens)
- [Entry Format](#entry-format)
- [Accepted Candidates](#accepted-candidates)
  - [`stat`](#stat)
  - [`ps` memory fields and sorting](#ps-memory-fields-and-sorting)
  - [`vmstat`](#vmstat)
- [Rejected / Deferred Candidates](#rejected--deferred-candidates)
  - [`top`](#top)
  - [`flock`](#flock)
  - [`systemd-tmpfiles`](#systemd-tmpfiles)

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

### `vmstat`

Type: new builtin

Decision: add broad GNU/procps-compatible investigation builtin; prioritize implementation after `free` and `ps` memory fields.

Evidence: the memory leak runbook uses `vmstat -s` to confirm host-level memory counters and `vmstat 2 10` to observe pressure over time. The cron storm runbook uses `vmstat 1 30` to check whether the run queue is staying above CPU capacity and whether the load is CPU-bound or I/O-bound.

Already covered? Not covered as a single host-pressure view. Existing or planned tools cover separate slices:
- `free` should be the simpler host-memory snapshot command, but it does not show runnable queues, blocked tasks, CPU split, I/O wait, or whether pressure is changing over repeated samples.
- `ps` identifies expensive processes by RSS or CPU, but it does not explain whether the host itself is saturated, paging, I/O-bound, or just running one hot process.
- `uptime` reports load averages, but not why load is high; `vmstat` adds the `r` and `b` queues plus CPU `us` / `sy` / `wa` / `st` breakdown.
- `top` is familiar but deferred because live terminal output is less deterministic for agents; `vmstat DELAY COUNT` gives a compact bounded time series.
- `df`, `du`, and `find` cover storage capacity and file growth, not runtime pressure from CPU scheduling, swap churn, or block I/O.

Coverage added: one bounded table correlating scheduler pressure (`r`, `b`), memory and swap (`free`, `buff`, `cache`, `si`, `so`), block I/O (`bi`, `bo`), interrupts/context switches (`in`, `cs`), and CPU state (`us`, `sy`, `id`, `wa`, `st`). That helps agents distinguish CPU-bound cron storms, disk-heavy jobs causing I/O wait, memory leaks causing paging, and general host saturation before choosing a more specific follow-up command.

Minimum subset: broad read-only GNU/procps `vmstat` compatibility for kernel-state visibility, including the default report, `-s`, and bounded `DELAY COUNT` sampling.

Target syntax:
- `vmstat -s`
- `vmstat 2 10`
- GNU/procps-compatible read-only display modes and formatting controls as the implementation grows.

🟢 Fit: complements `free` and `ps` by showing whether host memory pressure is turning into swap, I/O wait, CPU contention, or runnable/blocked queue growth.

🟢 Fit: single-shot output and count-bounded sampling are agent-friendly when unbounded live monitoring is rejected.

🟢 Fit: familiar Linux runbook command with strong diagnostic value for memory leaks and broader host-pressure investigations.

🔴 Scope: do not invoke the host `vmstat` binary, do not mutate kernel or filesystem state, and do not turn user operands such as device or partition names into file paths.

🔴 Platform: Linux/procfs first. Portable macOS/Windows support is deferred and should not block the initial candidate.

Implementation boundary: read only hardcoded kernel-state sources through the configured `ProcPath` or equivalent internal kernel readers. These reads intentionally bypass `AllowedPaths`, like `ss`, `ip route`, and `df`, because the opened paths are not derived from script input; document the operator-visibility trade-off in `README.md`, `SHELL_FEATURES.md`, and `AGENTS.md` when implemented. Treat device or partition operands as filters over kernel-reported entries, not filesystem paths to open. Reject unbounded sampling such as `vmstat 2`; require positive `DELAY` and `COUNT`, enforce an internal total-duration cap below the shell timeout, and respect context cancellation between samples.

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

### `flock`

Type: new remediation command candidate

Decision: do not add; high risk for the runbook remediation shape

Evidence: the cron storm runbook uses `flock -n /var/lock/<job>.lock <command>` and `flock -w 60 /var/lock/<job>.lock <command>` to prevent overlapping future cron runs. The important runbook path is wrapping the real scheduled job, for example `/path/to/job.sh`, not merely testing a lock with an inert builtin.

Already covered? Investigation is covered by accepted or planned read-only commands such as `ps`, `pgrep`, `uptime`, and `vmstat`. Immediate relief is a separate process-control problem (`kill` / `pkill`). Durable prevention through cron editing and job wrapping is not currently covered by rshell's remediation model.

Minimum subset: none initially.

Target syntax:
- Rejected: `flock -n /var/lock/myjob.lock /path/to/job.sh`
- Rejected: `flock -w 60 /var/lock/myjob.lock /path/to/job.sh`
- Lower-risk but insufficient for the runbook: `flock -n /var/lock/myjob.lock echo "got lock"`

🟢 Fit: directly addresses the overlapping-instance failure mode described in the cron storm runbook.

🔴 Agent fit: a successful command would hide a durable scheduling change behind a runtime wrapper. The script text alone does not prove that future cron launches are protected unless rshell also safely edits or validates the cron entry.

🔴 Scope: the runbook needs the external job-command form, which would require executing arbitrary host job scripts while holding a lock. Restricting execution to rshell builtins would only support verification, not the actual remediation.

🔴 Safety: implementing full `flock` semantics introduces lock-file creation/opening, advisory locking, blocking or wait-time behavior, nested command execution, file-descriptor locking forms, and `-c` shell-string execution. The external command and `-c` forms would bypass or greatly complicate rshell's existing command safety boundary.

🔴 Platform: Unix advisory locks and Windows file locking have materially different semantics, so a portable implementation would need careful platform-specific behavior.

Implementation boundary: do not invoke the host `flock` binary, do not add `-c` shell-string execution, do not support fd-oriented locking forms, and do not wrap arbitrary external job scripts. If this is revisited, prefer a separate, explicit cron-remediation design that can show the persistent schedule edit, require `AllowedPaths` `:rw` for any lock file or cron file touched, cap wait durations, respect context cancellation, and execute only commands permitted by the existing rshell command policy.

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
