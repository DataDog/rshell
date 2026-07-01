# Command Candidates

## Table Of Contents

- [Decision Lens](#decision-lens)
- [Entry Format](#entry-format)
- [Accepted Candidates](#accepted-candidates)
  - [`stat`](#stat)
  - [`lsof`](#lsof)
  - [`journalctl`](#journalctl)
  - [`free`](#free)
  - [`ps` memory fields and sorting](#ps-memory-fields-and-sorting)
  - [`pmap`](#pmap)
  - [`vmstat`](#vmstat)
  - [`uptime`](#uptime)
- [Rejected / Deferred Candidates](#rejected--deferred-candidates)
  - [`top`](#top)
  - [`pgrep`](#pgrep)
  - [`kill`](#kill)
  - [`crontab`](#crontab)
  - [`flock`](#flock)
  - [`nice`](#nice)
  - [`cleanup`](#cleanup)
  - [`rm`](#rm)
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

### `journalctl`

Type: new Linux/systemd investigation builtin with narrow remediation-mode support

Decision: add bounded journal inspection plus remediation-gated `--vacuum-size=SIZE`

Evidence: the remediation scenarios use `journalctl` across core dump investigation, crash-loop diagnosis, database retention cleanup history, unrotated log remediation, cron storm correlation, and OOM-kill checks. The unrotated / unbounded log runbook also uses `journalctl --disk-usage` and `journalctl --vacuum-size=500M` to recover space consumed by journald itself.

Already covered? Partially covered for adjacent signals. `df` and `du` can show that `/var/log` or journal directories are consuming disk, `cat` / `grep` can inspect syslog-style text logs and journald config files when paths are allowed, and `dmesg` can cover some kernel messages. Current rshell does not provide structured journald reads, service-unit filtering, reliable boot/kernel journal access, or a safe equivalent to `journalctl --vacuum-size`. `logrotate` does not manage journald binary journal files, `truncate` is unsafe for those files, and raw deletion is intentionally not exposed.

Minimum subset:
- Read-only investigation: `journalctl --disk-usage`, `journalctl -u UNIT -n N --no-pager`, `journalctl -u UNIT --since TIME`, `journalctl -k --since TIME`, `journalctl --since TIME`
- Remediation mode only: `journalctl --vacuum-size=SIZE`

Target syntax:
- `journalctl -u cron --since "2 hours ago" | grep -E "CMD|session"`
- `journalctl -u <service> -n 30 --no-pager`
- `journalctl -k --since "6 hours ago" | grep -i "killed process\|oom"`
- `journalctl --disk-usage`
- `journalctl --vacuum-size=500M`

🟢 Fit: closes repeated investigation gaps where agents need recent unit, kernel, crash, cron, OOM, or maintenance-job evidence from journald rather than only filesystem logs.

🟢 Fit: bounded journal reads with explicit unit, time, and tail filters are more agent-friendly than unbounded log scraping.

🟢 Fit: `--vacuum-size=SIZE` maps directly to the journald disk-recovery use case and is safer than exposing raw deletion of journal files because journald chooses old archived data to remove.

🔴 Scope: do not add full `journalctl` compatibility initially. Defer live follow (`-f`), cursor/export modes, JSON/output-format variants, boot selection, catalog output, arbitrary field queries, `--directory`, `--file`, `--root`, `--image`, and remote journal sources.

🔴 Remediation scope: reject `--vacuum-time`, `--vacuum-files`, arbitrary journal-file deletion, and journald configuration edits initially. Time-based and file-count vacuuming are easier for agents to misuse because they are less directly tied to the disk-headroom target than size-based vacuuming.

🔴 Visibility: journal reads can expose service logs, executable paths, usernames, hostnames, kernel messages, and application error content that may not be reachable through `AllowedPaths`.

Implementation boundary: do not invoke the host `journalctl` binary. Keep Linux/systemd support explicit and fail clearly elsewhere. Treat journal visibility as a deliberate host metadata boundary like `ps`, `ss`, `ip route`, `df`, and planned `vmstat`; document whether journal reads bypass `AllowedPaths` or require explicit allowed journal paths before implementation. For reads, require bounded output through a supported `--since`, `-n`, or equivalent cap, reject live streaming initially, cap line lengths and total rows, and respect context cancellation. For `--vacuum-size=SIZE`, require remediation mode, enforce a conservative minimum retained size, report before/after disk usage, document that historical journal entries are deleted, and avoid accepting user-controlled journal source paths until there is a separate design for their sandbox semantics.

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

### `pmap`

Type: new investigation builtin

Decision: add narrow read-only builtin for per-process memory-map diagnostics.

Evidence: the memory leak runbook uses `pmap -x <PID> | sort -k3 -rn | head -30` after `smaps_rollup` to identify the largest mappings inside a leaking process. This helps agents distinguish large anonymous heap regions from file-backed mappings without dumping the full per-VMA `smaps` file.

Already covered? Partially covered when operators expose procfs through `AllowedPaths`: agents can read `/proc/<PID>/maps`, `/proc/<PID>/smaps`, or `/proc/<PID>/smaps_rollup` directly. Raw proc files are noisy, large, and easy for agents to parse incorrectly. A narrow `pmap` gives a bounded, familiar table over the same operator-authorized proc data.

Minimum subset:
- `pmap -x PID`

Target syntax:
- `pmap -x <PID> | sort -k3 -rn | head -30`

🟢 Fit: closes a memory-leak investigation gap between aggregate process memory (`ps`, `/proc/<PID>/status`, `smaps_rollup`) and raw, verbose `/proc/<PID>/smaps` output.

🟢 Fit: output is read-only, single-process, table-shaped, and naturally bounded by pipelines such as `sort` and `head`, making it easier for agents to rank the mappings that matter.

🟢 Fit: when procfs is already exposed through `AllowedPaths`, `pmap` adds safer ergonomics and output normalization rather than new raw host authority.

🔴 Visibility: `pmap` exposes memory-map metadata such as address ranges, permissions, anonymous mapping labels, and mapped file paths. This can reveal deployment paths or runtime layout for the selected process.

🔴 Scope: do not add broad procps compatibility, all-process modes, argv/env disclosure, raw memory reads, or any behavior that writes files or invokes the host `pmap` binary.

🔴 Platform: Linux/procfs first. Portable macOS/Windows support is deferred and should not block the initial candidate.

Implementation boundary: do not make `pmap` a new unconditional `/proc` visibility bypass. Read procfs through the configured proc root only when that root is exposed by `AllowedPaths`; if procfs is not allowed, fail rather than silently expanding host visibility. Cap line lengths and mapping count, support one PID per invocation initially, respect context cancellation, and never read `/proc/<PID>/cmdline`, `/proc/<PID>/environ`, or process memory contents.

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

### `kill`

Type: new remediation command candidate

Decision: defer until rshell has a broader process-control / service-control design.

Evidence: the cron storm runbook uses `kill <PID-of-newer-instances>` for immediate relief when overlapping job instances are safe to stop, and uses `pkill -f <job-script-name>` when the job is safe to stop entirely. The runaway process runbook uses `kill <PID>`, `kill -9 <PID>`, and `kill -9 -$PGID` for unmanaged processes, but explicitly prefers `systemctl stop <service>` for systemd-managed services and warns that abrupt termination can drop in-flight work, leave on-disk state inconsistent, or destroy the primary diagnostic artifact.

Already covered? Investigation is partially covered by existing or accepted read-only commands such as `ps`, `vmstat`, `uptime`, and `journalctl`. Immediate process termination is not covered. For managed services, the safer remediation path points toward a service-control design rather than raw PID signalling. For cron storms, the broad `pkill -f` form depends on argv matching, which conflicts with the current `ps` / `pgrep` privacy boundary that avoids reading `/proc/<pid>/cmdline`.

Minimum subset: none initially. Deferred possible subset: remediation-only, PID-only `kill PID...` sending default `SIGTERM`, after rshell has an explicit process-control policy.

Target syntax:
- Deferred: `kill <PID>`
- Deferred initially: `kill -9 <PID>`
- Deferred initially: `kill -9 -<PGID>`
- Rejected as part of this candidate: `pkill -f <job-script-name>`

🟢 Fit: closes a real immediate-relief gap for unmanaged runaway processes and excess overlapping cron jobs.

🟢 Fit: a future PID-only default-`SIGTERM` subset could be explicit, bounded, and auditable when paired with remediation mode and process identity checks.

🔴 Safety: process signalling can cause downtime, lost in-flight work, inconsistent on-disk state, and loss of diagnostic evidence. `SIGKILL`, process-group kills, and broad pattern kills have much higher blast radius than default `SIGTERM` to a specific PID.

🔴 Scope: `AllowedPaths` cannot constrain process targets, so adding `kill` creates a new host-mutation boundary around PID reuse, target ownership, process identity validation, service auto-restart behavior, audit output, and OS permission failures.

🔴 Agent fit: the most convenient cron-storm form, `pkill -f`, requires argv matching and pattern-based termination. That is easy for agents to overmatch and would require a separate argv-disclosure decision.

Implementation boundary: do not invoke the host `kill` or `pkill` binaries. Do not add raw process signalling until a process-control / service-control design defines target validation, signal allowlist, remediation-mode gating, audit/reporting behavior, and how rshell should steer agents toward service-level controls when the target is systemd-managed.

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

### `nice`

Type: new remediation command candidate

Decision: do not add; only revisit if rshell gains explicit process-launch control.

Evidence: the cron storm runbook uses `nice -n 15 /path/to/job.sh` as a durable prevention step by editing future cron entries so legitimate jobs run with lower CPU scheduling priority.

Already covered? Investigation is covered by accepted or planned read-only commands such as `ps`, `uptime`, and `vmstat`. Durable cron remediation is intentionally not covered today; `crontab` persistent edits are deferred and `flock` job wrapping is rejected. If rshell later adds cron editing, it can write or validate cron text that invokes the host's `/usr/bin/nice`; that still does not require an rshell `nice` builtin because cron will launch the host command later, outside rshell. `nice` also does not provide immediate relief for already-running cron storms; existing hot processes would require a separate process-control primitive such as `renice`, `kill`, or cgroup/systemd CPU controls.

Minimum subset: none initially.

Target syntax:
- Rejected: `nice -n 15 /path/to/job.sh`
- Rejected initially: `nice COMMAND [ARG]...`
- Not an rshell builtin target: cron entry text such as `* * * * * /usr/bin/nice -n 15 /path/to/job.sh`
- Lower-risk but low-value subset: `nice` wrapping only other rshell builtins, if rshell ever supports priority-aware builtin process launch

🟢 Fit: useful operator guidance, or future cron-remediation template text, for reducing the CPU scheduling priority of legitimate cron work without disabling the job.

🔴 Agent fit: the valuable runbook form is a host command wrapper applied to future cron launches. If rshell edits the cron entry, the remediation artifact is the persistent schedule diff, not an rshell `nice` invocation.

🔴 Scope: implementing `nice` inside rshell is only useful if rshell itself launches or wraps processes. Wrapping `/path/to/job.sh` would require executing arbitrary external job scripts or adding process-launch control semantics, which is outside rshell's builtin-only safety model.

🔴 Remediation value: `nice` changes scheduling priority but does not cap CPU, prevent overlapping instances, stop fan-out, reduce memory use, or throttle disk I/O. It is weaker than `flock` for overlap prevention and weaker than cgroup/systemd controls for hard CPU limits.

Implementation boundary: do not add an rshell `nice` builtin merely to support cron editing. A future cron-remediation design may emit, validate, or diff cron entries containing `/usr/bin/nice`, but should leave execution to the host cron daemon. Only revisit an rshell `nice` builtin if rshell adopts explicit process-launch control; in that case, do not invoke the host `nice` binary, do not wrap arbitrary external commands without a broader launch policy, and explain when softer scheduling priority is sufficient versus when hard CPU limits or overlap prevention are required.

### `cleanup`

Type: new remediation builtin candidate

Decision: defer dedicated deletion-oriented cleanup primitive pending a separate deletion-safety design.

Evidence: disk remediation runbooks need deletion behavior that is more structured than raw `rm`: keep at least one recent core dump while deleting older dumps, delete stale rotated logs without touching active logs, delete temp/session files older than a safe TTL, and recover inodes from large numbers of stale small files.

Already covered? Partially covered for byte recovery by `truncate` and `logrotate`, which can reclaim space from explicit active files through `AllowedPaths` `:rw`. Not covered for inode recovery or stale-file deletion: raw `rm` is not exposed, `find -delete` is blocked, and `systemd-tmpfiles` / `coredumpctl clean` are rejected as broad host-policy engines rather than rshell-native cleanup primitives.

Minimum subset: deferred. If accepted later, start with regular-file cleanup only; recursive directory deletion should require a separate decision.

Target syntax:
- Deferred: exact syntax is not accepted yet.
- Possible future shape: `cleanup --dry-run --path /var/lib/systemd/coredump --type f --keep-newest 1`
- Possible future shape: `cleanup --dry-run --path /var/log/<service> --type f --name "*.log.[0-9]*"`
- Possible future shape: `cleanup --dry-run --path /tmp --max-depth 2 --type f --mtime +1`

🟢 Fit: deletion is a real remediation gap for disk and inode incidents where truncation cannot recover the relevant resource.

🟢 Fit: an rshell-native cleanup command can make destructive remediation explicit and auditable instead of inheriting silent POSIX `rm` behavior.

🟢 Fit: regular-file cleanup maps directly to the highest-confidence runbook cases: core dumps, rotated logs, temp files, and stale session files.

🔴 Safety: deletion is irreversible and can destroy forensic evidence, active session state, undelivered work, or files still needed by running processes.

🔴 Scope: exact predicates, dry-run output, deletion limits, traversal behavior, symlink handling, and partial-failure semantics materially affect safety and should be designed before accepting implementation.

🔴 Scope: recursive directory deletion is not part of the initial candidate; build-artifact directory cleanup (`node_modules`, `__pycache__`) has a larger blast radius and should be evaluated separately.

Implementation boundary: do not implement `cleanup` as an alias or wrapper around host `rm`, `find`, `systemd-tmpfiles`, or `coredumpctl`. If revisited, require remediation mode, `AllowedPaths` `:rw`, dry-run/reporting, explicit path operands, regular-file defaults, no final symlink following, traversal and count limits, and context cancellation.

### `rm`

Type: new remediation command candidate

Decision: do not add raw `rm`; defer deletion-oriented remediation to a dedicated future design.

Evidence: disk remediation runbooks use deletion to recover space or inodes: the core-dump flood runbook uses `rm -f /var/crash/*` and deletion of all but the most recent systemd core dump; the unrotated / unbounded log runbook uses `rm -f` for old rotated log files; the inode exhaustion runbook uses `find ... -delete`, `xargs rm -f`, and `rm -rf` examples for stale small files or build artifacts.

Already covered? Investigation is mostly covered by `df`, `du`, `find`, `ls`, `sort`, `head`, and planned `stat`. Remediation is intentionally only partially covered: `truncate` and `logrotate` recover byte space from explicit active files through `AllowedPaths` `:rw`, but they do not remove stale files and do not recover inodes consumed by many small files. Raw deletion is currently not exposed: `rm` is rejected as unknown and `find -delete` is blocked for sandbox safety. The separate deferred `cleanup` candidate is the preferred direction for any future deletion support.

Minimum subset: none for raw `rm`. The deferred `cleanup` candidate may start with explicit regular-file cleanup only; recursive directory deletion is deferred.

Target syntax:
- Rejected: `rm -f /var/crash/*`
- Rejected: `rm -f /var/log/<service>/*.log.[0-9]*`
- Rejected: `rm -rf /app/**/__pycache__`
- Deferred separate candidate: an rshell-native cleanup helper with explicit path operands, predicates, dry-run/reporting, and deletion limits.

🟢 Fit: deletion is a real remediation gap for stale core dumps, old rotated logs, and inode exhaustion where truncation is insufficient.

🟢 Fit: a future cleanup primitive could make deletion explicit, auditable, and constrained while still covering high-value disk runbook cases.

🔴 Safety: `rm` is irreversible, can destroy forensic evidence, and has high blast radius when combined with globs or recursive flags.

🔴 Agent fit: the command name carries broad POSIX expectations (`rm -rf`, silent success with `-f`, recursive tree deletion) that are stronger than the narrow remediation behavior rshell should expose.

🔴 Scope: even a narrow `rm -f FILE...` subset would either surprise users by rejecting common forms or pressure rshell toward broader deletion semantics. It also does not encode runbook safeguards such as keeping the newest dump, deleting only stale files older than a TTL, or reporting exactly what would be removed.

Implementation boundary: do not invoke the host `rm` binary, do not add an `rm` alias for a safer cleanup helper, and do not enable `find -delete` as part of this candidate. If deletion is revisited, evaluate it as a separate remediation design with explicit operands, dry-run output, `AllowedPaths` `:rw` enforcement, regular-file defaults, no final symlink following, traversal and count limits, context cancellation, and a separate decision before recursive directory deletion.

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
