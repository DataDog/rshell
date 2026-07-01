# Command Candidates

## Table Of Contents

- [Decision Lens](#decision-lens)
- [Entry Format](#entry-format)
- [Accepted Candidates](#accepted-candidates)
  - [`stat`](#stat)
  - [`lsof`](#lsof)
  - [`journalctl`](#journalctl)
  - [`systemctl`](#systemctl)
  - [`free`](#free)
  - [`ps` memory fields and sorting](#ps-memory-fields-and-sorting)
  - [`pmap`](#pmap)
  - [`vmstat`](#vmstat)
  - [`uptime`](#uptime)
- [Rejected / Deferred Candidates](#rejected--deferred-candidates)
  - [`top`](#top)
  - [`strace`](#strace)
  - [`perf`](#perf)
  - [`dmesg`](#dmesg)
  - [`pgrep`](#pgrep)
  - [`kill`](#kill)
  - [`crontab`](#crontab)
  - [`flock`](#flock)
  - [`nice`](#nice)
  - [`cleanup`](#cleanup)
  - [`rm`](#rm)
  - [`systemd-tmpfiles`](#systemd-tmpfiles)
  - [`coredumpctl`](#coredumpctl)
  - [`sysctl`](#sysctl)
  - [`tee`](#tee)
  - [`tune2fs`](#tune2fs)
- [Scenario Gap Report](#scenario-gap-report)

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

### `systemctl`

Type: new Linux/systemd investigation builtin with narrow service-control remediation support

Decision: add bounded systemd unit inspection plus remediation-gated `start`, `stop`, `restart`, and `reload` for explicitly allowed units.

Evidence: the remediation scenarios use `systemctl` to inspect crash loops, failed units, restart counts, maintenance timers, logrotate timers, kdump/crash service state, and post-remediation service health. They also use service control to stop crash loops or runaway services, restart leaking services, reload database services, release deleted log file descriptors, restart services after configuration changes, and start services after a fix is deployed.

Already covered? Not covered. `ps` can show process state, `journalctl` can show service logs, and filesystem commands can inspect unit/config files when paths are allowed, but current rshell has no safe systemd unit-state view, no timer listing, no failed-unit discovery, no restart-count query, and no managed-service remediation path. Raw `kill` is deferred and is not a substitute for systemd-managed services because it can trigger auto-restart behavior and bypass unit lifecycle semantics.

Minimum subset:
- Read-only investigation: `systemctl status UNIT`, `systemctl show UNIT --property=NRestarts`, `systemctl --state=failed`, `systemctl list-timers`
- Remediation mode only, for explicitly allowlisted units: `systemctl start UNIT`, `systemctl stop UNIT`, `systemctl restart UNIT`, `systemctl reload UNIT`

Target syntax:
- `systemctl --state=failed`
- `systemctl status <service>`
- `systemctl status logrotate.timer`
- `systemctl show <service> --property=NRestarts`
- `systemctl list-timers | grep logrotate`
- `systemctl restart <service>`
- `systemctl stop <service>`
- `systemctl start <service>`
- `systemctl reload mysql`

🟢 Fit: closes a repeated investigation gap for discovering failed units, checking timer health, confirming restart loops, and verifying service state after remediation.

🟢 Fit: service-level `start` / `stop` / `restart` / `reload` is safer for systemd-managed processes than raw PID signalling because it uses the unit lifecycle and can suppress or apply manager restart semantics intentionally.

🟢 Fit: a narrow remediation subset maps directly to the scenario evidence without accepting persistent unit-policy changes.

🔴 Scope: do not add full `systemctl` compatibility initially. Defer `mask`, `unmask`, `enable`, `disable`, `edit`, `daemon-reload`, `daemon-reexec`, arbitrary `show`, `list-units`, job management, environment changes, unit file writes, and user-manager / remote-machine modes.

🔴 Remediation scope: service-control verbs can cause downtime, dropped in-flight requests, session loss, or log collection gaps. They must require remediation mode and a trusted unit allowlist rather than allowing any script-supplied unit name.

🔴 Visibility: read-only unit and timer listings expose host service names, unit state, process identifiers, result codes, timestamps, and scheduling metadata that may not be reachable through `AllowedPaths`.

Implementation boundary: keep Linux/systemd support explicit and fail clearly elsewhere. Initial `status` output should be metadata-only: no journal tail, no process tree, and no argv disclosure. Treat systemd unit visibility as a deliberate host metadata boundary like `ps`, `ss`, `ip route`, `df`, `journalctl`, and planned `vmstat`; document whether it bypasses `AllowedPaths` or requires a separate unit allowlist before implementation. Keep output bounded, cap list rows, respect context cancellation, and do not implement this as a raw wrapper around the full host `systemctl` command.

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

### `strace`

Type: new privileged Linux process-observation builtin candidate

Decision: do not add initially; defer until rshell has an explicit privileged process-observation policy.

Evidence: the runaway process / infinite loop runbook uses `strace -p <PID> -c -f -e trace=all -- sleep 5` to sample syscall activity after identifying a single spinning PID. The output helps distinguish a pure userspace CPU loop from a lock spin, I/O retry loop, or a process that is actually waiting in `epoll_wait` / `select`.

Already covered? Partially covered by safer triage signals. `ps`, `/proc/<PID>/wchan`, `/proc/<PID>/stat`, `vmstat`, and `uptime` can identify a sustained CPU consumer and distinguish userspace CPU from broader host pressure. The accepted `systemctl` candidate gives a safer remediation path for systemd-managed services. These do not provide syscall-frequency evidence, but the current scenario uses that evidence for deeper diagnosis rather than the minimum incident triage path.

Minimum subset: none initially. Deferred possible subset: PID-targeted, attach-only, duration-bounded, output-capped syscall summary.

Target syntax:
- Rejected initially: `strace -p <PID> -c -f -e trace=all -- sleep 5`
- Rejected initially: `strace -p <PID> -e trace=all`
- Deferred possible subset: a narrow attach-only syscall summary for one validated PID and a short fixed duration

🟢 Fit: useful when operators need to preserve diagnostic evidence before terminating a runaway process and need to distinguish pure CPU spin from syscall-heavy retry, lock, or I/O behavior.

🔴 Scenario weight: appears in one scenario as a deeper root-cause diagnostic, so it should not displace lower-risk CPU investigation work.

🔴 Privilege / reliability: attaching to another process generally requires `CAP_SYS_PTRACE` or matching ownership plus permissive ptrace settings, and is commonly blocked in containers or hardened hosts.

🔴 Agent fit: real `strace` output is noisy, potentially unbounded, and easy for agents to over-collect unless rshell defines duration limits, syscall filters, row caps, and output shaping.

🔴 Visibility / perturbation: ptrace can expose syscall arguments, file paths, network endpoints, signals, timing, and application data fragments that are not constrained by `AllowedPaths`; attaching can also perturb or slow the target process.

🔴 Scope: do not add command-launch tracing, output-file modes, arbitrary `-e` expressions, full syscall argument dumps, unbounded live tracing, or broad process-tree tracing initially.

Implementation boundary: do not invoke the host `strace` binary. Do not add ptrace/process attachment until a privileged process-observation design defines target validation, PID identity checks, ownership and capability behavior, output redaction and caps, context cancellation, and how rshell should prevent PID reuse or wrong-process attachment.

### `perf`

Type: new privileged Linux investigation builtin candidate

Decision: do not add initially; defer until rshell has repeated profiling evidence or an explicit privileged profiling mode.

Evidence: the runaway process / infinite loop runbook uses `perf top -p <PID>` only as an optional deep CPU profiling step after identifying the spinning process with `top` / `ps`, checking `/proc/<PID>/wchan`, and sampling syscall activity with `strace`.

Already covered? Partially covered by safer triage signals. `ps` process sorting, `/proc/<PID>/stat`, `/proc/<PID>/wchan`, `vmstat`, and `uptime` can identify a single sustained CPU consumer and distinguish userspace CPU spin from broader host pressure. They do not identify the hot function inside the process, but that is developer root-cause evidence rather than required incident triage/remediation evidence.

Minimum subset: none initially.

Target syntax:
- Rejected initially: `perf top -p <PID>`
- Rejected initially: `perf top -p <PID> -d 5`

🟢 Fit: useful when operators need function-level CPU profiling before terminating a runaway process.

🔴 Scenario weight: appears in only one scenario and only as optional deep profiling, so it should not displace lower-risk CPU investigation work.

🔴 Privilege / reliability: practical use often requires `CAP_PERFMON`, `CAP_SYS_ADMIN`, or permissive `perf_event_paranoid` settings, and is commonly blocked in containers.

🔴 Agent fit: `perf top` is a live profiler rather than a naturally bounded, deterministic table. Making it agent-friendly would require rshell-specific duration limits, sampling limits, and output shaping.

🔴 Visibility: perf sampling can expose kernel/user symbols, mapped library paths, addresses, and execution hotspots that are not constrained by `AllowedPaths`.

🔴 Scope: full `perf` compatibility would pull in system-wide profiling, recording files, tracepoints, probes, call graphs, event selection, and kernel-version-specific behavior. That is much broader than the current runbook need.

Implementation boundary: do not invoke the host `perf` binary. Do not add `perf_event_open` access until rshell has an explicit privileged profiling design. If revisited, start Linux-only, PID-targeted, read-only, duration-bounded, and output-capped; reject system-wide profiling, record/report file generation, tracepoints, kprobes/uprobes, eBPF, call graphs, and arbitrary event selection initially.

### `dmesg`

Type: new investigation builtin candidate

Decision: do not add initially; prefer accepted `journalctl -k` for kernel-message investigation and revisit only if non-systemd / journald-unavailable hosts become an explicit target.

Evidence: the core dump, slow leak, unbounded cache, and GC-pressure scenarios use `dmesg | grep -i "oom..."` or `dmesg | grep -i "killed process"` to check whether the kernel OOM killer terminated a process.

Already covered? Mostly covered by the accepted `journalctl` candidate. `journalctl -k --since TIME` provides the same kernel-message investigation path for systemd/journald hosts, with stronger time bounding and more agent-friendly filtering. `dmesg` would add value mainly on minimal or non-systemd Linux hosts where journald is unavailable.

Minimum subset: none initially. Deferred possible subset: read-only kernel-ring-buffer display only, with bounded output.

Target syntax:
- Rejected initially: `dmesg | grep -i "out of memory\|oom_kill\|killed process"`
- Preferred alternative: `journalctl -k --since "6 hours ago" | grep -i "killed process\|oom"`
- Deferred possible subset, if non-systemd support becomes necessary: `dmesg`

🟢 Fit: familiar Linux investigation command for confirming OOM kills, kernel panics, driver errors, and other kernel-originated signals.

🟢 Fit: can cover Linux hosts without systemd/journald if rshell later decides that environment is important.

🔴 Redundancy: the planned `journalctl -k` path already covers the current scenario evidence while also supporting time filters and journal metadata.

🔴 Visibility: kernel logs can expose device names, host configuration, process names, usernames, addresses, driver messages, and application-adjacent error content that may not be reachable through `AllowedPaths`.

🔴 Privilege / reliability: modern Linux deployments commonly restrict kernel-ring-buffer reads to privileged users. `dmesg` output is also volatile and may lose older OOM evidence, while journald can retain timestamped kernel messages across a longer incident window.

🔴 Scope: do not add `dmesg` just because runbooks mention it as a familiar alias for kernel logs; adding both `journalctl -k` and `dmesg` creates overlapping host-log visibility boundaries.

Implementation boundary: do not invoke the host `dmesg` binary. If revisited, make Linux-only support explicit, reject ring-buffer mutation such as clear/read-clear modes, reject live follow initially, cap output, respect context cancellation, and document that kernel-log reads intentionally bypass `AllowedPaths` because they expose host kernel state rather than user-selected filesystem paths.

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

### `sysctl`

Type: new kernel-parameter investigation/remediation builtin candidate

Decision: do not add initially; reject generic `sysctl` reads/writes and defer any narrow `kernel.core_pattern` remediation until rshell has a broader core-dump remediation design.

Evidence: the core-dump flood runbook uses `sysctl -w kernel.core_pattern='|/bin/false'` to temporarily stop future dump generation while a crash loop is being fixed, then uses another `sysctl -w kernel.core_pattern=...` command to restore the original dump handler. The command reference mentions `sysctl` only for Core Dump Flood remediation.

Already covered? Investigation is covered by `cat /proc/sys/kernel/core_pattern` when that proc path is allowed. The safer immediate remediation is to stop the crash loop with `systemctl stop <service>` before cleaning dumps. Durable prevention is better expressed as explicit service/systemd configuration changes such as `LimitCORE=0`, `DefaultLimitCORE=0`, or coredump storage limits. The remaining gap is only a temporary, host-wide `kernel.core_pattern` write.

Minimum subset: none initially.

Target syntax:
- Rejected: `sysctl -a`
- Rejected: `sysctl KEY`
- Rejected: `sysctl -w KEY=VALUE`
- Rejected: `sysctl -p`
- Rejected: `sysctl --system`
- Deferred possible subset, only if core-dump flood remains a priority gap: a remediation-mode, allowlisted way to set `kernel.core_pattern` for the crash-flood workflow.

🟢 Fit: temporarily redirecting core dumps can stop future dump files while preserving the current running system and avoiding additional service disruption.

🔴 Scenario weight: current evidence is a single remediation step in one scenario, not a repeated investigation or remediation pattern.

🔴 Safety: `kernel.core_pattern` is host-wide. Changing it can suppress forensic artifacts for unrelated crashes, and restoring the wrong value can permanently alter crash handling.

🔴 Agent fit: generic `sysctl` is an unbounded key/value interface over kernel state. Agents can easily choose plausible but dangerous keys that are unrelated to the scenario.

🔴 Scope: full `sysctl` compatibility would include arbitrary reads, arbitrary writes, config-file loading via `-p` / `--system`, platform-specific namespaces, and persistent host-policy interaction. That is much broader than the runbook need.

Implementation boundary: do not invoke the host `sysctl` binary and do not expose arbitrary `/proc/sys` reads or writes through a builtin. If revisited, avoid a generic `sysctl` surface; design a narrow core-dump remediation primitive or an allowlisted `kernel.core_pattern` operation with remediation-mode gating, explicit before/after reporting, original-value guidance, Linux-only behavior, and documentation that the write bypasses `AllowedPaths` because it mutates host kernel state rather than a user-selected filesystem path.

### `tee`

Type: new remediation-capable pipeline builtin candidate

Decision: do not add initially; defer until scenarios show a need for write-through pipeline capture.

Evidence: no current remediation scenario uses `tee`, and no current scenario requires preserving stdout while also writing the same stream to a file.

Already covered? Mostly covered by remediation-mode `>` / `>>` for file writes, plus `truncate` and `logrotate` for explicit log remediation. Not covered: copying a pipeline stream to a file while continuing to pass the stream downstream.

Minimum subset: none initially.

Target syntax:
- Rejected initially: `cmd | tee FILE`
- Rejected initially: `cmd | tee -a FILE`
- Preferred alternative when stdout preservation is unnecessary: `cmd > FILE` or `cmd >> FILE`

🟢 Fit: familiar command for capturing diagnostic output while continuing a pipeline.

🔴 Evidence: no scenario demand under this repo's candidate decision lens.

🔴 Safety: `tee` creates a second file-write surface outside shell redirection syntax, so it must duplicate remediation-mode gating, `AllowedPaths` `:rw` checks, no-symlink write handling, streaming limits, partial-failure semantics, and cancellation behavior.

🔴 Scope: even narrow `tee [-a] FILE...` has multi-file writes, binary streaming, broken-pipe behavior, infinite-input handling, and cross-platform write semantics.

Implementation boundary: do not invoke the host `tee` binary. If revisited, require remediation mode for file operands, enforce `AllowedPaths` `:rw` and no-symlink write semantics exactly like file redirections, reject unsupported GNU extensions, stream with bounded buffers, and respect context cancellation.

### `tune2fs`

Type: new Linux/ext filesystem command candidate

Decision: do not add initially; prefer path- and mount-based inode diagnostics through `df` and planned `stat`.

Evidence: the inode exhaustion runbook uses `tune2fs -l /dev/sda1 | grep -i inode` to inspect total and free inode counts for an ext filesystem. This is the only scenario evidence for `tune2fs`.

Already covered? Mostly covered by `df -i` / `df -ih` for mount-level inode usage and the accepted `stat -f PATH...` candidate for path-targeted total/free inode checks. The larger remaining gap in the same runbook is `du --inodes`, not ext superblock inspection.

Minimum subset: none initially.

Target syntax:
- Rejected: `tune2fs -l /dev/sda1 | grep -i inode`
- Preferred alternatives: `df -i`, `df -ih`, `stat -f /var/spool/`

🟢 Fit: read-only `-l` can expose ext2/3/4 superblock fields, including inode counts, in a familiar operator format.

🔴 Scope: `tune2fs` is primarily a filesystem tuning command with many mutating flags. Supporting only `-l` is surprising, while supporting broader compatibility would create a high-risk filesystem administration surface.

🔴 Safety: the useful operand is a user-selected block device such as `/dev/sda1`. Exposing direct device reads would either require expanding `AllowedPaths` semantics to block devices or creating a new host-device visibility bypass unlike hardcoded kernel-state readers such as `df`, `ss`, and `ip route`.

🔴 Value: the incident answer is duplicated by `df -i` plus planned `stat -f`, and the command is Linux/ext-specific rather than a general filesystem diagnostic.

Implementation boundary: do not invoke the host `tune2fs` binary and do not add a generic block-device superblock parser. If revisited, restrict the design to read-only Linux ext2/3/4 metadata, reject all mutating flags, define explicit block-device sandbox semantics, and document why the `df` / `stat -f` path is insufficient.

## Scenario Gap Report

This report compares `candidates.md` with the remediation runbooks under `scenarios/` and calls out important commands or command families that are used by the scenarios but do not currently have candidate entries. It intentionally does not override the decisions above; it is a triage list for future candidate records.

### High-Impact Gaps

#### `docker` command family

Scenario evidence: Docker disk scenarios repeatedly depend on `docker system df`, `docker system prune`, `docker buildx du`, `docker buildx prune`, `docker images`, `docker ps`, and `docker volume prune`.

Impact: high. These commands are central to diagnosing and remediating Docker image, build-cache, stopped-container, and orphaned-volume disk pressure. The scenarios distinguish Docker daemon-reported reclaimable space from raw `du` output, and in the overlay2 case `docker system df` is the authoritative view.

Coverage gap: not covered by current candidates. Existing `df`, `du`, `find`, and `ls` can show that `/var/lib/docker` is large, but they cannot safely identify reclaimable Docker objects or perform daemon-aware pruning.

Candidate direction: add a dedicated `docker` family candidate, likely with read-only investigation first (`docker system df`, `docker images`, `docker ps`, `docker volume ls`, `docker buildx du`) and remediation-mode-only prune operations if the safety model can support Docker-daemon authority.

#### `psql` and PostgreSQL remediation queries

Scenario evidence: database growth, orphaned PostgreSQL replication slot, and long-running database transaction scenarios use `psql` for database introspection and remediation, including `pg_replication_slots`, `pg_stat_activity`, `pg_cancel_backend`, `pg_terminate_backend`, `pg_drop_replication_slot`, `VACUUM`, `REINDEX`, `ALTER SYSTEM`, and `pg_reload_conf()`.

Impact: high. These workflows can stop disk growth, unblock VACUUM, release WAL retention, and terminate harmful sessions, but the remediation actions can also roll back in-flight work, invalidate replicas or CDC consumers, lock tables, or alter persistent database policy.

Coverage gap: not covered by current candidates. Filesystem commands can show WAL or database directories growing, but they cannot identify the database object or session causing the growth, and they cannot perform database-native cleanup.

Candidate direction: add a `psql` / PostgreSQL candidate even if the decision is to reject or defer most behavior. A useful candidate record should separate read-only monitoring queries from high-risk remediation queries and should explicitly address credential handling, query allowlisting, output bounds, and whether rshell should ever execute database-mutating SQL.

#### `mysql`

Scenario evidence: the database-growth scenario uses `mysql` to inspect and purge binary logs, including `SHOW BINARY LOGS`, `SHOW VARIABLES LIKE ...`, `PURGE BINARY LOGS`, and possible `SET GLOBAL binlog_expire_logs_seconds`.

Impact: medium-high. MySQL binary log accumulation can fill the database volume, and `PURGE BINARY LOGS` can reclaim space without service downtime when used correctly. Misuse can break replication or remove logs needed for recovery.

Coverage gap: not covered by current candidates. Existing filesystem commands can show `/var/lib/mysql` growth, but they cannot distinguish table data from binary logs or apply database-native retention changes.

Candidate direction: add a `mysql` candidate or include it in a broader database-client candidate. As with PostgreSQL, separate read-only introspection from remediation and document why arbitrary SQL execution is likely outside the initial rshell scope.

#### Process `/proc` introspection

Scenario evidence: memory and CPU scenarios directly use `/proc/<PID>/status`, `/proc/<PID>/smaps_rollup`, `/proc/<PID>/stat`, `/proc/<PID>/wchan`, and `/proc/<PID>/fd` counts. Existing accepted candidates partially cover this through `ps` memory fields and `pmap`, but not all scenario needs.

Impact: high for investigation, low for remediation. These reads are key for distinguishing memory leaks, GC pressure, thread growth, kernel wait state, and file-descriptor growth. They are also naturally bounded when narrowed to one PID.

Coverage gap: partially covered, but not explicitly tracked as a candidate. Raw `cat` and `ls` can read these paths only when procfs paths are allowed, and raw `/proc` formats are easy for agents to parse incorrectly. `pmap` covers memory maps, but not `wchan`, selected status fields, fd counts, or CPU tick sampling from `stat`.

Candidate direction: add a narrow process-introspection candidate or explicitly document that these remain path-based reads under `AllowedPaths`. A candidate should avoid argv/env disclosure unless rshell deliberately changes its process privacy boundary.

### Lower-Priority Gaps

#### `sleep` and `watch`

Scenario evidence: several runbooks use `sleep` in polling loops and `watch` for repeated visual checks of RSS, socket counts, WAL size, and core-dump creation.

Impact: medium. Repeated measurement is useful for verification and trend confirmation, but it can often be replaced by bounded commands such as `vmstat DELAY COUNT` or explicit short loops.

Candidate direction: consider a bounded `sleep` builtin if shell loops are intended to be first-class. Defer or reject `watch` unless rshell wants a general repeated-command surface with strict interval, count, timeout, and output caps.

#### Package and build cache tools: `npm`, `pip`, `gradle`, `go`

Scenario evidence: the temp-file and build-artifact scenario uses `npm cache verify`, `npm cache clean --force`, `pip cache info`, `pip cache purge`, `gradle --stop`, and `go clean -modcache`.

Impact: medium. These commands can reclaim build-host cache space, but they are ecosystem-specific and usually affect future build speed rather than immediate production service health. `gradle --stop` can kill in-progress builds.

Candidate direction: defer initially. Prefer generic filesystem investigation plus a future rshell-native cleanup primitive for stale cache directories before adding package-manager-specific CLIs.

#### Mail queue commands: `postqueue` and `postsuper`

Scenario evidence: the inode-exhaustion scenario uses `postqueue -p` to inspect queue depth and `postsuper -d ALL deferred` to delete deferred mail.

Impact: narrow but high-risk. Mail queue deletion can recover inodes, but `postsuper -d ALL deferred` irreversibly drops undelivered mail.

Candidate direction: reject initially or add an explicit deferred/rejected candidate if mail-queue inode exhaustion becomes a recurring priority. Do not expose broad mail-queue mutation without a dedicated policy and confirmation model.

#### Destructive filesystem administration: `mkfs.ext4`

Scenario evidence: inode exhaustion mentions `mkfs.ext4 -i 4096 /dev/sdb1` as a long-term filesystem reformatting option.

Impact: very high risk, low rshell fit. Reformatting a filesystem requires outage planning, device targeting, backups, and human approval.

Candidate direction: explicitly reject if it is likely to be proposed. It should remain outside rshell remediation scope.
