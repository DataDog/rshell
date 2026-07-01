# CPU - Runaway Process / Infinite Loop

**Signal:** `system.cpu.user` sustained high (80–100%) attributable to a single PID; does not resolve on its own  
**IssueType:** `cpu_usage`  
**Metric (typical):** `system.cpu.user`, `system.load.1`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`top`| investigation| `top -o %CPU` · `top -p <PID>`  
`ps`| investigation| `ps aux --sort=-%cpu \| head -10` · `ps -p <PID> -o pid,ppid,cmd,pcpu,etime`  
`/proc/<PID>/wchan`| investigation| `cat /proc/<PID>/wchan`  
`/proc/<PID>/stat`| investigation| `awk '{print "utime=" $14 " stime=" $15}' /proc/<PID>/stat`  
`strace`| investigation| `strace -p <PID> -c -f` · `strace -p <PID> -e trace=all`  
`perf`| investigation| `perf top -p <PID>`  
`uptime`| investigation| `uptime`  
`vmstat`| investigation| `vmstat 1 10`  
`kill`| remediation| `kill <PID>` · `kill -9 <PID>`  
`systemctl`| remediation| `systemctl stop <service>` · `systemctl status <service>`  
  
* * *

## What Happens

A process enters a loop that does not terminate and continuously consumes one CPU core (or more if multi-threaded). Unlike a cron storm (many short-lived processes sharing CPU), this is a single long-lived process pinned at high CPU indefinitely. Unlike GC pressure (CPU and memory rise together), CPU is high but memory is typically stable.

Common causes:

  * A logic bug where a loop termination condition is never met (off-by-one error, concurrent write invalidating the exit condition, expected event never firing)

  * Tight retry logic with no backoff that loops on a transient error (e.g., retrying a failed network call in a `while true` without sleep)

  * Recursive processing of a cyclic data structure (a graph or linked list with a cycle that the traversal code does not detect)

  * A race condition where a thread spins checking a flag that is never set by another thread

  * A misconfigured event loop that re-fires immediately instead of waiting for I/O




* * *

## Detection

Detected via `system.cpu.user` sustained above the monitor threshold without decaying. The distinguishing characteristic is a single PID consistently at or near 100% CPU for minutes or hours — a flat, continuous, indefinite plateau. Unlike a cron storm (spikes and decays) or GC pressure (bursty with memory correlation), this does not self-resolve.

**Correlated signals to check:**

  * `system.load.1` elevated but not at extreme multiples of core count (one runaway thread saturates one core, not all)

  * Application request latency rising: if the runaway process is the application itself, all threads compete with the spinning thread for CPU time

  * Application monitor going to NO_DATA or ALERT if the event loop is blocked by the spinning thread

  * No corresponding `system.mem.used` rise: if memory is also climbing, consider GC pressure instead




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Ability to run `top`, `ps`, `strace`| Standard tools; `strace` requires root or `CAP_SYS_PTRACE`  
`perf` installed if deep CPU profiling is needed| Optional; available via `linux-tools-common` / `perf` package  
  
### Steps

  1. **Identify the spinning PID**



    
    
    top -o %CPU
    # Look for a single process with CPU% near 100 that does not decay
    # Note: on a multi-core host, 100% = one full core; 400% = four cores
    
    ps aux --sort=-%cpu | head -10
    # Confirms PID, username, exact command, and how long it has been running (TIME column)

  2. **Confirm it is a single long-running process**



    
    
    ps -p <PID> -o pid,ppid,cmd,pcpu,etime
    # ETIME: a process running for > 5 minutes at 100% CPU is a runaway
    # Short ETIME that repeats = process is spawning repeatedly (cron storm pattern instead)

  3. **Check what the process is doing at the kernel level**



    
    
    # What kernel function is the process sleeping in (if any)?
    cat /proc/<PID>/wchan
    # Common values:
    #   0 or "running": in userspace — tight CPU loop with no kernel wait
    #   futex_wait_queue_me: waiting on a mutex (possible spin or deadlock)
    #   do_select / ep_poll: waiting for I/O (not a CPU loop)
    
    # Syscall frequency: high = active I/O; near-zero = pure userspace CPU loop
    strace -p <PID> -c -f -e trace=all -- sleep 5

  4. **Sample the hot code path (requires**`perf`)



    
    
    perf top -p <PID> -d 5
    # Shows which functions are consuming CPU cycles
    # The top symbol usually identifies the looping function

  5. **Check CPU time breakdown (userspace vs kernel)**



    
    
    # Fields 14 (utime) and 15 (stime) in clock ticks
    awk '{print "utime=" $14 " stime=" $15}' /proc/<PID>/stat
    # Take two samples 10 s apart; if utime grows fast but stime is flat: pure userspace loop

  6. **Check thread count and state**



    
    
    ls /proc/<PID>/task | wc -l
    cat /proc/<PID>/status | grep Threads
    # If many threads all in 'R' state: multi-threaded spin

  7. **Determine if the process is systemd-managed**



    
    
    systemctl status <service-name> 2>/dev/null
    # If Restart=always, killing the process immediately respawns it; use systemctl stop instead

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Confirm the PID belongs to the expected process before killing| Killing the wrong process causes unplanned downtime  
Check `systemctl status` to understand restart behavior| If `Restart=always`, a `kill` immediately respawns the process; use `systemctl stop` instead  
Collect `strace` or `perf` output before killing if root cause analysis is needed| The spinning process is the primary diagnostic artifact; killing it destroys the evidence  
Note whether the process holds critical state| An abrupt kill may leave data in an inconsistent state  
  
### Immediate Relief

**For a systemd-managed service:**
    
    
    # Stop the service cleanly (suppresses auto-restart)
    systemctl stop <service-name>
    
    # Verify it stopped
    systemctl status <service-name>
    
    # Restart when ready
    systemctl start <service-name>

**For an unmanaged process:**
    
    
    # Graceful termination first
    kill <PID>
    
    # Confirm the process is gone
    sleep 3 && ps -p <PID>
    
    # If SIGTERM is ignored (common in tight loops):
    kill -9 <PID>   # SIGKILL — cannot be caught or ignored

**For a multi-threaded runaway:**
    
    
    # Kill the entire process group
    PGID=$(ps -o pgid= -p <PID> | tr -d ' ')
    kill -9 -$PGID

### Prevent Recurrence

**Add a CPU quota to cap blast radius while the fix is developed:**
    
    
    # /etc/systemd/system/<service>.service.d/cpu-limit.conf
    [Service]
    CPUQuota=200%   # Max 2 full cores; adjust to expected peak
    
    
    systemctl daemon-reload
    systemctl restart <service-name>

With `CPUQuota` set, a runaway loop is throttled automatically without killing the process, preserving it for diagnosis.

**Fix the underlying loop condition.** OS-level data to hand to the developer:

Observation| Likely code path  
---|---  
Near-zero syscalls, `wchan = 0`| Pure CPU loop; look for `while(true)` or missing termination condition  
High `futex` syscall rate| Spin on a lock; look for a mutex that is never released  
High `read`/`write` syscall rate| I/O retry loop; look for retry logic without backoff  
  
* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`top`, `ps`, `strace`, `perf top`| None| Read-only; `strace` attaches to the process but does not stop it  
`systemctl stop <service>`| **Service goes down**|  Clean shutdown; auto-restart suppressed; requires explicit `systemctl start` to restore  
`kill <PID>` (SIGTERM)| **Process terminates**|  Graceful if the signal is handled; in-flight requests may be dropped  
`kill -9 <PID>` (SIGKILL)| **Abrupt process termination**|  No cleanup; in-flight requests lost; on-disk state may be inconsistent  
`kill -9 -<PGID>`| **Entire process group terminated**|  All child processes killed simultaneously  
Adding `CPUQuota` \+ `daemon-reload`| None| Takes effect on next service start  
Adding `CPUQuota` \+ restart| **Brief service interruption**|  Same as restart  
  
`systemctl stop` is always preferred over `kill -9` for systemd-managed services: it runs `ExecStop`, flushes in-flight work, and suppresses auto-restart until you are ready.

* * *

## Verification
    
    
    # Confirm CPU has recovered
    top -b -n 1 | head -10
    # The spinning PID should no longer appear near the top
    
    uptime
    # Load average should drop below CPU core count within 1-2 minutes
    
    # If service was restarted, confirm it is healthy
    systemctl status <service-name>

In Datadog, verify:

  * `system.cpu.user` drops to baseline after the process is terminated

  * `system.load.1` recovers within 1–2 minutes

  * The application service monitor returns to OK state

  * CPU does not immediately climb back to 100% — if it does, the service restarted with the same bug still present; disable auto-restart and investigate before re-enabling




* * *

## Related Scenarios

  * If CPU immediately returns to 100% after a restart, disable auto-restart (`systemctl edit <service>` → `Restart=no`) and set `CPUQuota` before re-enabling.

  * If `strace` shows the process is mostly waiting in `epoll_wait` or `select` (I/O wait, not a CPU spin), the high CPU reading may be misleading; check `system.cpu.iowait` and focus on the I/O subsystem instead.

  * If multiple PIDs are each near 100%, see the Cron Storm scenario.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6844973492 -->
