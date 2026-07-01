# Command Candidates

## Table Of Contents

- [Decision Lens](#decision-lens)
- [Entry Format](#entry-format)
- [Accepted Candidates](#accepted-candidates)
  - [`stat`](#stat)
  - [`lsof`](#lsof)
  - [`free`](#free)
  - [`ps` memory fields and sorting](#ps-memory-fields-and-sorting)
  - [`vmstat`](#vmstat)
  - [`uptime`](#uptime)
- [Rejected / Deferred Candidates](#rejected--deferred-candidates)
  - [`top`](#top)
  - [`pgrep`](#pgrep)
  - [`crontab`](#crontab)
  - [`flock`](#flock)
  - [`systemd-tmpfiles`](#systemd-tmpfiles)
  - [`coredumpctl`](#coredumpctl)

## Decision Lens

LLM-based agents are the main users of rshell. Candidate decisions should optimize rshell's usefulness for AI agents resolving investigations: commands should be easy for agents to choose correctly, produce bounded and explainable output, expose enough host context to diagnose issues, and keep remediation actions explicit, auditable, and constrained by rshell's safety model.

## Entry Format

This is a concise decision record for generally useful investigation commands. Each entry should focus on the candidate decision only: accept, defer, or reject. Do not evaluate the implementation design here; implementation details, parser behavior, platform-specific mechanics, and code-level boundaries should be handled in a later implementation plan. Use 🟢 for reasons to support it and 🔴 for reasons to reject, defer, or narrow the scope, so the decision is easy to scan.

Each entry should include: type, decision, evidence, existing coverage, minimum subset, target syntax, and fit/scope rationale. Mention implementation constraints only when they materially affect the accept/defer/reject decision. Put accepted or planned work under "Accepted Candidates" and rejected or deferred work under "Rejected / Deferred Candidates". Rejected alternatives should usually live inside the related candidate entry; add standalone rejected entries only when the command is likely to be proposed again.

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

### `lsof`

Type: new investigation builtin

Decision: add narrow builtin for deleted-open file diagnostics

Evidence: the unrotated / unbounded log runbook uses `lsof | grep deleted | grep log` during investigation and verification to find deleted-but-open log files whose directory entries are gone but whose disk blocks remain allocated until the owning process releases the file descriptor.

Already covered? Not covered. `df` can show that disk space is still consumed, while `du`, `find`, and `ls` cannot see an unlinked file. `ss` is not a substitute because it reports socket state rather than open regular files, and this shell intentionally rejects `ss -p` process disclosure.

Minimum subset: deleted-open regular file diagnostics only. Exact syntax and implementation design are deferred.

Target syntax:
- Deferred: `lsof` workflow for deleted-open files, equivalent to the runbook's `lsof | grep deleted | grep -i log`

🟢 Fit: closes a real disk-space investigation gap where existing filesystem commands cannot identify the process holding reclaimed-looking space.

🟢 Fit: read-only, bounded process/file-descriptor metadata is agent-friendly when scoped to deleted-open files instead of a full host-wide open-file inventory.

🔴 Scope: do not add full `lsof`. General FD listing, socket inspection, argv disclosure, network modes, and mutation-oriented behavior are outside this candidate.

🔴 Visibility: even the narrow diagnostic shape exposes process-owned file-descriptor metadata and paths that may be outside `AllowedPaths`; that host-visibility trade-off must be documented when implementation is designed.

### `free`

Type: new builtin

Decision: add narrow read-only investigation builtin

Evidence: the memory leak runbook uses `free -h` to confirm host-level memory pressure before narrowing to a leaking process, and uses `free -h` again during verification to confirm that available memory recovered after remediation.

Already covered? Not covered by a simple host-memory snapshot. `ps` identifies high-memory processes, and `vmstat` explains whether host pressure is turning into swap, I/O wait, CPU contention, or queue growth, but neither replaces the quick total/used/free/available/swap view that agents need at the start and end of a memory investigation.

Minimum subset:
- `free`
- `free -h`

Target syntax:
- `free -h`

🟢 Fit: bounded, read-only, familiar output for confirming whether the host is under memory pressure.

🟢 Fit: complements `ps` memory fields and `vmstat`; it gives the first-pass host snapshot, while those commands explain process ownership and pressure dynamics.

🔴 Scope: do not treat `free` as a remediation command or as the primary time-series pressure tool. Repeated sampling and trend interpretation belong with `vmstat` or higher-level telemetry.

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

### `uptime`

Type: new builtin

Decision: add narrow read-only investigation builtin; prioritize after `vmstat` and `ps` CPU/process sorting.

Evidence: the cron storm runbook uses `uptime` to check whether load average remains above CPU capacity during investigation and whether load has recovered during verification. The signal is also generally useful across host-pressure investigations as a quick "is this host broadly under pressure?" context check.

Already covered? Partially covered by `vmstat`, which is more diagnostic because it explains scheduler pressure, blocked tasks, CPU split, I/O wait, and swap behavior. `uptime` is still useful as a cheaper single-shot summary and verification command, but it should not be the primary root-cause tool.

Minimum subset: `uptime`

Target syntax:
- `uptime`

🟢 Fit: bounded, read-only, familiar output that gives agents quick host context before or after deeper pressure investigation.

🟢 Fit: complements `vmstat` and `ps` by providing a compact load-average snapshot for triage and post-remediation verification.

🔴 Scope: load average alone is easy to overinterpret; documentation and examples should steer agents to `vmstat` and `ps` when they need to explain why load is high.

🔴 Priority: lower value than `vmstat` and `ps` CPU/process sorting because it does not identify the cause of pressure.

Implementation boundary: do not invoke the host `uptime` binary. Keep the builtin read-only and focused on host context; defer GNU/procps formatting details and optional fields to the implementation design.

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

### `pgrep`

Type: new investigation builtin candidate

Decision: do not add initially; defer until rshell has a clear argv-disclosure policy or repeated evidence that agents need comm-only PID lookup semantics.

Evidence: the cron storm runbook uses `pgrep -a -f <job-script-name>` to find overlapping cron job instances by matching the full command line and printing PID plus argv.

Already covered? The runbook-compatible behavior is not covered because current `ps` intentionally exposes only process comm/executable names and does not read argv. A safe comm-only subset is mostly covered by `ps -e` plus `grep`, and the accepted `ps` custom-column enhancement should make the preferred workflow more explicit with `ps -e -o pid,ppid,comm,etime`.

Minimum subset: none initially.

Target syntax:
- Rejected initially: `pgrep -a -f <job-script-name>`
- Deferred safe subset, if needed later: `pgrep PATTERN`, `pgrep -x PATTERN`, `pgrep -l PATTERN`
- Preferred alternative: `ps -e -o pid,ppid,comm,etime | grep <process-name>`

🟢 Fit: familiar, bounded PID lookup command with useful scripting exit codes.

🟢 Fit: a comm-only implementation could reuse the existing process provider without introducing new host read surfaces.

🔴 Runbook gap: the valuable cron-storm form is `-f`, because cron jobs often appear as `sh`, `bash`, `python`, or another interpreter in the comm field; matching the script path requires argv.

🔴 Safety: supporting `-f` or `-a` as expected would require reading and optionally printing `/proc/<pid>/cmdline`, which breaks the current `ps` privacy boundary that avoids exposing command-line secrets.

🔴 Scope: a safe comm-only `pgrep` adds convenience and exit-code semantics but little new diagnostic power over `ps`, so it should not displace higher-value `ps` field and sorting work.

Implementation boundary: do not invoke the host `pgrep` binary, do not read `/proc/<pid>/cmdline`, and do not support full-command matching or argv output unless rshell explicitly adopts and documents an argv-disclosure mode. If comm-only `pgrep` is revisited, match only against `procinfo.ProcInfo.Cmd`, keep supported flags explicit and small, cap output by the existing process-list cap, and preserve standard no-match exit semantics.

### `crontab`

Type: new investigation builtin candidate

Decision: do not add initially; defer read-only `crontab -l`, reject persistent cron edits.

Evidence: the cron storm runbook uses `crontab -l` to correlate CPU spikes with per-user cron schedules. Remediation examples require editing schedules to add `flock`, add `nice`, or stagger jobs, but those are durable host mutations rather than simple command inspection.

Already covered? System cron inspection is mostly covered by existing file-reading commands when paths are allowed: `cat /etc/crontab`, `cat /etc/cron.d/*`, and `ls /etc/cron.hourly/ /etc/cron.daily/ /etc/cron.weekly/`. The unique gap is current-user crontab spool visibility via `crontab -l`.

Minimum subset: none initially. Deferred possible subset: `crontab -l` for the current user only.

Target syntax:
- Deferred: `crontab -l`
- Rejected: `crontab -e`
- Rejected: `crontab FILE`
- Rejected: `crontab -r`
- Rejected initially: `crontab -l -u USER`

🟢 Fit: useful for correlating recurring CPU spikes with per-user cron schedules.

🔴 Incremental value: after excluding writes, most safe cron inspection is already possible through `cat`, `ls`, and `grep` over allowed cron files.

🔴 Safety: persistent edits can silently change future host behavior, disable unrelated jobs, or install recurring command execution. This is higher risk than existing remediation builtins such as `truncate` and `logrotate`.

🔴 Scope: user crontab storage and daemon reload behavior vary across platforms and distributions. `-u USER` also introduces identity and privilege semantics.

Implementation boundary: do not invoke the host `crontab` binary, do not edit or install crontabs, do not remove crontabs, and do not support `-u` until rshell has a clear user-identity policy. If revisited, start with read-only current-user listing, bounded output, and explicit documentation that durable cron remediation needs a separate design with dry-run diff, backup, strict parser, rollback/audit story, and remediation-mode gating.

### `flock`

Type: new remediation command candidate

Decision: do not add; high risk for the runbook remediation shape

Evidence: the cron storm runbook uses `flock -n /var/lock/<job>.lock <command>` and `flock -w 60 /var/lock/<job>.lock <command>` to prevent overlapping future cron runs. The important runbook path is wrapping the real scheduled job, for example `/path/to/job.sh`, not merely testing a lock with an inert builtin.

Already covered? Investigation is covered by accepted or planned read-only commands such as `ps`, `uptime`, and `vmstat`. Immediate relief is a separate process-control problem (`kill` / `pkill`). Durable prevention through cron editing and job wrapping is not currently covered by rshell's remediation model.

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

### `coredumpctl`

Type: new investigation builtin candidate

Decision: do not add initially; defer read-only `list` / `info`, reject dump extraction, debugger execution, and cleanup/remediation behavior.

Evidence: the core-dump flood runbook uses `coredumpctl list` to identify recent crashes by timestamp, PID, executable, signal, and corefile state, and uses `coredumpctl info` to inspect the most recent dump during root-cause analysis. The same runbook also lists cleanup/remediation forms, but those cross into deletion and disk-recovery behavior rather than bounded investigation.

Already covered? Disk-flood triage is mostly covered by existing commands: `find /var/crash /var/lib/systemd/coredump -mmin -10 -ls` confirms recent dump creation, `ls -lhtr ... | tail` shows newest dump files, `du -sh` measures dump-directory impact, `df -h` checks remaining capacity, and `cat /proc/sys/kernel/core_pattern` verifies where dumps are written. The missing value is systemd-journal crash metadata: executable path, PID, UID/GID, signal, and whether the corefile is present, missing, truncated, or journal-only.

Minimum subset: none initially. Deferred possible subset: `coredumpctl list` and `coredumpctl info` only, with bounded row/time filters.

Target syntax:
- Deferred: `coredumpctl list`
- Deferred: `coredumpctl list -n 20`
- Deferred: `coredumpctl info`
- Rejected: `coredumpctl dump`
- Rejected: `coredumpctl debug`
- Rejected: `coredumpctl gdb`
- Rejected: `coredumpctl --output=FILE dump`
- Rejected: any cleanup/deletion behavior such as `coredumpctl clean --disk-free 5G`
- Preferred disk-triage alternatives: `find /var/crash /var/lib/systemd/coredump -mmin -10 -ls`, `du -sh /var/crash/ /var/lib/systemd/coredump/`, `df -h /var/crash`, `cat /proc/sys/kernel/core_pattern`

🟢 Fit: the read-only `list` / `info` shape is familiar, bounded, and useful when an agent must identify which executable is crash-looping and what signal produced the core dump.

🔴 Incremental value: for the disk-space alert itself, existing file and filesystem commands already confirm active dump growth, estimate fill rate, measure reclaimed headroom, and verify dump destinations.

🔴 Scope: `dump` can emit very large binary core files, `--output` writes files, and `debug` / `gdb` execute an external debugger. These do not fit rshell's bounded, builtin-only investigation model.

🔴 Safety: cleanup/remediation belongs in a separate rshell-native cleanup design with explicit paths, dry-run output, `AllowedPaths` `:rw` enforcement, and deletion/traversal limits. Do not treat `coredumpctl` as the cleanup primitive.

🔴 Platform: Linux/systemd-journal-only. Unlike `/proc`-backed commands, rshell does not yet have an explicit systemd journal metadata access boundary.

Implementation boundary: do not invoke the host `coredumpctl` binary, do not read or emit raw core payloads, do not create output files, and do not spawn debuggers. If revisited, first define a journald/systemd metadata boundary: data source, field allowlist, whether journal reads bypass `AllowedPaths` or require explicit allowed journal paths, platform behavior, output caps, and privacy expectations for executable paths and user/group identifiers.
