# Memory - Slow Application Leak

**Signal:** `system.mem.used` rising steadily over hours or days; sawtooth pattern where the post-collection memory floor rises with each cycle  
**IssueType:** `memory_usage`  
**Metric (typical):** `system.mem.used`, `system.mem.pct_usable`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`free`| investigation| `free -h`  
`ps`| investigation| `ps aux --sort=-%mem \| head -20`  
`top`| investigation| `top -o %MEM` · `top -b -n 1 -p <PID>`  
`/proc/<PID>/status`| investigation| `cat /proc/<PID>/status \| grep -E 'VmRSS\|VmSize\|VmSwap'`  
`smaps_rollup`| investigation| `cat /proc/<PID>/smaps_rollup`  
`pmap`| investigation| `pmap -x <PID> \| sort -k3 -rn \| head -20`  
`vmstat`| investigation| `vmstat -s` · `vmstat 2 10`  
`ls /proc/<PID>/fd`| investigation| `ls /proc/<PID>/fd \| wc -l`  
`dmesg`| investigation| `dmesg \| grep -i "oom\|killed process"`  
`systemctl`| remediation| `systemctl restart <service>`  
  
* * *

## What Happens

A process allocates memory over its lifetime and fails to release it. At OS level this manifests as the process RSS (Resident Set Size) growing continuously regardless of application load. If the runtime uses a garbage collector, the metric shows a sawtooth pattern: the collector periodically reclaims reachable-but-unused memory, but leaked objects (still referenced by a long-lived structure) survive every collection cycle. The diagnostic signature is that the post-collection floor rises over time — each trough of the sawtooth is higher than the last.

Common causes:

  * Objects held in a static map, registry, or event listener list that is never drained

  * A queue or buffer that grows on writes but is never consumed or trimmed

  * File descriptors, sockets, or database connections opened but never closed

  * Per-request allocations stored in a cache with no TTL or size limit

  * Native memory allocated outside the managed heap (JNI, FFI, direct byte buffers) that is not released




* * *

## Detection

The platform detects this via `system.mem.used` or `system.mem.pct_usable` breaching the monitor threshold. The defining characteristic is trend shape: a slow, continuous rise over hours or days, not a sudden spike. On hosts with GC-based runtimes the metric shows a rising sawtooth; on non-GC runtimes it trends smoothly upward.

**Correlated signals to check:**

  * `system.mem.pct_usable` falling below 10% — at this point the kernel reclaims pages aggressively and application latency rises

  * `system.swap.used` rising — the process is being paged to disk, a late-stage indicator

  * Application error spans mentioning `OutOfMemoryError`, `cannot allocate memory`, or `malloc failed`

  * OOM kill events: `dmesg | grep -i "killed process"` shows which PID was terminated




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Read access to `/proc` filesystem| Required to inspect per-process memory maps  
Ability to run `ps`, `top`, `dmesg`| Standard tools; available on all Linux distributions  
  
### Steps

  1. **Confirm host-level memory pressure**



    
    
    free -h
    # Check "available" column — below 500 MB on most hosts indicates pressure
    
    vmstat -s | grep -E "total memory|free memory|used memory"

  2. **Identify the highest-RSS process**



    
    
    ps aux --sort=-%mem | head -20
    # Columns: %MEM (% of RAM), RSS (resident set in KB), VSZ (virtual)
    # Focus on processes with large RSS that are long-running

  3. **Track RSS growth over time**



    
    
    # Poll RSS every 30 seconds
    PID=<pid>
    while true; do
      rss=$(awk '/VmRSS/{print $2}' /proc/$PID/status)
      echo "$(date +%T)  RSS=${rss} kB"
      sleep 30
    done
    # A leak shows consistent upward drift; a stable process fluctuates around a mean

  4. **Inspect anonymous vs file-backed memory**



    
    
    # Summary breakdown
    cat /proc/<PID>/smaps_rollup
    # Key fields:
    #   Anonymous:  heap allocations not backed by a file — where leaks live
    #   Shared_Clean/Shared_Dirty: shared library pages (normally stable)
    #   Swap: how much of this process is paged out
    
    # Detailed mapping list — large anonymous regions that grow over time indicate heap leak
    pmap -x <PID> | sort -k3 -rn | head -30

  5. **Check for file descriptor leaks**



    
    
    ls /proc/<PID>/fd | wc -l
    # Healthy long-running process: stable count
    # Leaking process: count rises continuously
    
    cat /proc/sys/fs/file-nr
    # Shows: allocated / unused / max system-wide fds

  6. **Check for OOM kills**



    
    
    dmesg | grep -i "out of memory\|oom_kill\|killed process"
    journalctl -k --since "6 hours ago" | grep -i "killed process\|oom"

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Identify the leaking process before acting| Restarting the wrong service causes unnecessary disruption  
Coordinate with the service owner if the process holds critical in-flight state| An uncoordinated restart drops in-flight requests  
The underlying leak must be fixed separately| A restart recovers memory temporarily; the leak recurs on the same schedule  
  
### Immediate Memory Recovery

**Restart the leaking service:**
    
    
    systemctl restart <service-name>
    systemctl status <service-name>   # confirm it came up healthy

**If a restart cannot happen immediately, steer the OOM killer away from critical services:**
    
    
    # Make the leaking process the preferred OOM kill target
    echo 500 > /proc/<leaking-PID>/oom_score_adj
    
    # Protect a co-located critical process
    echo -500 > /proc/<critical-PID>/oom_score_adj
    # Range: -1000 (never kill) to 1000 (kill first)

### Prevent Runaway Growth

**Cap memory and enable auto-restart via systemd:**
    
    
    # /etc/systemd/system/<service>.service.d/memory-limit.conf
    [Service]
    MemoryMax=2G
    MemorySwapMax=0
    Restart=on-failure
    RestartSec=10s

Apply with:
    
    
    systemctl daemon-reload
    systemctl restart <service-name>

With `MemoryMax` set, the kernel OOM-kills the process when the limit is breached and systemd restarts it automatically. This bounds the impact while the fix is developed.

### Fix the Underlying Leak

OS-level data narrows the search before handing off to application-level profiling:

OS observation| Likely cause  
---|---  
RSS grows at constant rate independent of traffic| Background thread or timer-triggered allocation  
RSS grows proportionally to request rate| Per-request object not freed  
FD count grows alongside RSS| Unclosed file descriptors or sockets  
`smaps_rollup` Anonymous grows; file-backed stable| Heap leak (not a library or mmap issue)  
`system.swap.used` rising| Leak has been ongoing long enough to exhaust RAM  
  
* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Polling `/proc/<PID>/status`, `ps`, `pmap`| None| Read-only inspection  
Adjusting `oom_score_adj`| None| Changes OOM priority only; process keeps running  
`systemctl restart <service>`| **Brief service interruption**|  In-flight requests are dropped; drain the load balancer first if possible  
Adding `MemoryMax` \+ `systemctl daemon-reload`| None| `daemon-reload` does not restart services; limit applies on next start  
Adding `MemoryMax` \+ `systemctl restart`| **Brief service interruption**|  Same as restart above  
**Kernel OOM kill** (uncontrolled)| **Abrupt termination**|  No graceful shutdown; in-flight requests lost; on-disk state may be incomplete  
  
A coordinated `systemctl restart` behind a load balancer drain is always safer than waiting for a kernel OOM kill.

* * *

## Verification
    
    
    # Confirm memory recovered
    free -h   # "available" should have increased
    
    # Confirm RSS is stable post-restart (not immediately climbing)
    PID=$(pgrep -f <service-name>)
    watch -n 10 "awk '/VmRSS/{print \$2}' /proc/$PID/status"
    
    # Confirm no new OOM events
    dmesg | grep -i "oom" | tail -5

In Datadog, verify:

  * `system.mem.used` drops sharply after the restart and remains flat (no immediate re-climb)

  * `system.mem.pct_usable` recovers above the warning threshold

  * The sawtooth floor does not resume rising — if it does, the restart was mitigation only and the leak fix has not yet been deployed




* * *

## Related Scenarios

  * If RSS climbs back to the threshold within hours, set `MemoryMax` and let the systemd restart loop contain the impact while the fix is developed.

  * If FD count is growing, treat as a descriptor leak first; both RSS and fd exhaustion will independently crash the process.

  * If the process's memory growth correlates with unique user or session counts rather than being constant over time, see the Unbounded Cache / Session Growth scenario.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6846513498 -->
