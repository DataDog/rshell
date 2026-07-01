# Memory - Unbounded Cache / Session Growth

**Signal:** `system.mem.used` rising steadily, correlated with user/session count or uptime; no sawtooth; growth rate decelerates or plateaus when traffic drops  
**IssueType:** `memory_usage`  
**Metric (typical):** `system.mem.used`, `system.mem.pct_usable`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`free`| investigation| `free -h`  
`ps`| investigation| `ps aux --sort=-%mem \| head -20`  
`/proc/<PID>/status`| investigation| `cat /proc/<PID>/status \| grep -E 'VmRSS\|VmSize'`  
`smaps_rollup`| investigation| `cat /proc/<PID>/smaps_rollup`  
`ss`| investigation| `ss -tp \| grep <service>` · `ss -s`  
`ls /proc/<PID>/fd`| investigation| `ls /proc/<PID>/fd \| wc -l`  
`vmstat`| investigation| `vmstat 2 10`  
`dmesg`| investigation| `dmesg \| grep -i "oom\|killed process"`  
`systemctl`| remediation| `systemctl restart <service>`  
  
* * *

## What Happens

An in-process cache, session store, or lookup table grows without eviction. Unlike a slow leak — which is typically unintentional and grows at a constant rate — unbounded cache growth is often by design but misconfigured: a cache with no TTL or no maximum entry count, a session map that accumulates entries for every visitor but never expires them, a connection pool that grows to meet demand but never shrinks.

At OS level the RSS of the process grows proportionally to the number of distinct cached keys or active sessions, not at a fixed rate over time. Growth slows when traffic drops and accelerates when traffic rises. The key distinguishing test vs a slow leak is whether growth rate tracks request rate or unique-object count.

Common causes:

  * An application cache (in-memory hash map, LRU cache configured without a max size) accumulating entries without eviction

  * HTTP session storage in memory with no session timeout configured

  * A per-connection or per-request context object stored in a registry that is never deregistered after the connection closes

  * A metrics or telemetry buffer that accumulates data points faster than they are flushed

  * A pub/sub subscriber list that grows as new clients connect but never shrinks when they disconnect




* * *

## Detection

Detected via `system.mem.used` or `system.mem.pct_usable` breaching the monitor threshold. The signature differs from a slow leak in two ways: the growth rate tracks traffic or session count rather than being constant, and there is typically no sawtooth (no periodic reclaim, because the objects are all reachable and the GC cannot reclaim them).

**Correlated signals to check:**

  * `system.mem.pct_usable` trending down over days rather than hours (unbounded caches often grow slower than active leaks under typical load)

  * Application request rate metrics: if memory growth decelerates when traffic drops, the growth is session- or request-driven

  * Open socket count from `ss -s`: a growing `ESTABLISHED` count that never decreases suggests session objects are accumulating alongside open connections

  * `system.mem.swap.used` rising as a late-stage indicator




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Read access to `/proc` filesystem| Required for per-process memory and fd inspection  
Application metrics or logs accessible| Correlating memory growth with request rate requires application-level signal  
  
### Steps

  1. **Confirm host-level memory pressure**



    
    
    free -h
    vmstat -s | grep -E "total memory|free memory"

  2. **Identify the growing process**



    
    
    ps aux --sort=-%mem | head -20
    # Note the process with highest RSS and longest uptime

  3. **Track RSS and correlate with traffic**



    
    
    PID=<pid>
    while true; do
      rss=$(awk '/VmRSS/{print $2}' /proc/$PID/status)
      echo "$(date +%T)  RSS=${rss} kB"
      sleep 60
    done
    # If RSS growth accelerates during traffic spikes and slows overnight: session/cache-driven
    # If growth is constant regardless of traffic: more likely a classic leak

  4. **Check socket and connection count**



    
    
    # Count connections associated with the process
    ss -tp | grep <service-name> | wc -l
    
    # Overall socket summary
    ss -s
    # ESTABLISHED count: if this grows without bound, sessions are not being closed

  5. **Check file descriptor count**



    
    
    ls /proc/<PID>/fd | wc -l
    # If this grows continuously alongside ESTABLISHED socket count, connection objects are retained

  6. **Inspect anonymous memory breakdown**



    
    
    cat /proc/<PID>/smaps_rollup
    # Anonymous: should be the growing region for heap-held caches
    # If file-backed memory is growing, check for mmap-backed stores (e.g., SQLite WAL, RocksDB)

  7. **Check for OOM events**



    
    
    dmesg | grep -i "out of memory\|killed process"

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Identify the specific process before acting| Restarting the wrong service has no effect  
For session stores: confirm whether session data loss on restart is acceptable| Some services require session persistence; coordinate with the owning team  
The underlying cache or session configuration must be fixed| A restart recovers memory but growth resumes immediately after  
  
### Immediate Memory Recovery

**Restart the process to flush the accumulated cache or session state:**
    
    
    systemctl restart <service-name>
    systemctl status <service-name>

Note: a restart flushes all in-memory sessions. For applications with user-facing session state (logged-in users, shopping carts), this causes a forced logout for all active users. Coordinate accordingly.

### Prevent Runaway Growth

**Set a memory limit and enable auto-restart:**
    
    
    # /etc/systemd/system/<service>.service.d/memory-limit.conf
    [Service]
    MemoryMax=4G
    MemorySwapMax=0
    Restart=on-failure
    RestartSec=10s
    
    
    systemctl daemon-reload
    systemctl restart <service-name>

### Fix the Underlying Configuration

The permanent fix is application-side, but OS-level observations guide the right fix:

OS observation| Application-side fix  
---|---  
RSS growth tracks unique session count| Add session TTL and maximum concurrent session limit  
RSS growth tracks request rate| Add a max-size eviction policy to the request-scoped cache  
ESTABLISHED socket count growing| Fix connection lifecycle: ensure close/release is called on disconnect  
FD count growing alongside RSS| Fix descriptor lifecycle: ensure file handles are closed after use  
RSS grows but request rate is flat| Growth is likely tied to unique keys (e.g., per-user or per-IP caches); add TTL  
  
* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`ps`, `ss`, `/proc` inspection| None| Read-only  
`systemctl restart <service>`| **Brief service interruption + session loss**|  In-flight requests dropped; all in-memory sessions lost; active users are logged out  
Adding `MemoryMax` \+ `daemon-reload`| None| Limit applies on next start only  
Adding `MemoryMax` \+ restart| **Brief service interruption + session loss**|  Same as restart above  
**Kernel OOM kill**| **Abrupt termination**|  No graceful shutdown  
Fixing cache TTL / max-size in config| None at config time| Requires a service restart to take effect  
  
**Session loss is the primary coordination concern** for user-facing services. For internal or stateless services a restart is low-risk.

* * *

## Verification
    
    
    # Confirm memory recovered after restart
    free -h
    
    # Confirm RSS is no longer growing proportionally to traffic
    PID=$(pgrep -f <service-name>)
    watch -n 30 "awk '/VmRSS/{print \$2}' /proc/$PID/status"
    
    # Confirm socket count is stable
    watch -n 10 "ss -tp | grep <service-name> | wc -l"

In Datadog, verify:

  * `system.mem.used` drops after the restart

  * Memory growth rate is flat or logarithmic (cache filling to steady state) rather than linear

  * If a cache TTL was added: memory should plateau at a stable level proportional to active sessions within the TTL window




* * *

## Related Scenarios

  * If growth rate is constant regardless of traffic, the issue is more likely a slow leak; see the Slow Application Leak scenario.

  * If memory recovers after a restart but re-fills within minutes, the cache fill rate is very high; a size cap is the more effective near-term control than a TTL.

  * If socket count is growing but RSS is stable, connections may be leaking at the OS level without retaining application-level session data; focus investigation on the connection lifecycle rather than the cache.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6845333920 -->
