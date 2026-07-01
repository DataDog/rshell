# CPU - GC Pressure (Runtime Collector Thrashing)

**Signal:** `system.cpu.user` elevated in a long-running process; rises over time or stays persistently high; correlated with `system.mem.used` sawtooth  
**IssueType:** `cpu_usage`  
**Metric (typical):** `system.cpu.user`, `system.mem.used`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`top`| investigation| `top -p <PID>` · `top -o %CPU`  
`ps`| investigation| `ps -p <PID> -o pid,cmd,pcpu,pmem,etime`  
`/proc/<PID>/stat`| investigation| `awk '{print "utime=" $14 " stime=" $15}' /proc/<PID>/stat`  
`/proc/<PID>/status`| investigation| `cat /proc/<PID>/status \| grep -E 'VmRSS\|VmSize'`  
`vmstat`| investigation| `vmstat 1 30`  
`find`| investigation| `find /var/log /tmp /opt -name "*.log" -path "*gc*" 2>/dev/null`  
`grep`| investigation| `cat /proc/<PID>/cmdline \| tr '\\0' ' '`  
`dmesg`| investigation| `dmesg \| grep -i "oom\|killed process"`  
`systemctl`| remediation| `systemctl restart <service>`  
  
* * *

## What Happens

A runtime's garbage collector is spending an increasing fraction of CPU time trying to reclaim memory. At OS level, the process's CPU consumption rises without a corresponding increase in application throughput. The memory metric simultaneously shows a rising sawtooth: the heap fills, the collector runs (consuming CPU), reclaims some memory, and the cycle restarts — but each cycle the heap fills faster or to a higher floor than before.

As heap pressure increases, the collector runs more frequently and for longer. At the extreme ("GC thrashing"), the process spends more CPU time collecting than running application code, and throughput collapses despite high CPU usage.

Common causes:

  * Heap size is too small for the current workload — the configured max heap is frequently hit and the collector is forced to run at high frequency

  * Allocation rate has increased (traffic spike, batch job, data ingestion surge) without a corresponding heap headroom increase

  * A memory leak is slowly reducing available heap space, forcing more frequent collections (GC pressure is masking an underlying leak)

  * Short-lived but large objects allocated at high rate, overwhelming the young generation collector

  * Objects surviving collection longer than expected (large retained object graphs, long-lived request contexts)




* * *

## Detection

Detected via `system.cpu.user` sustained above the monitor threshold in a long-running process, correlated with `system.mem.used` showing a sawtooth pattern. The distinguishing characteristics from other CPU scenarios:

  * CPU and memory metrics move together: when memory rises toward the heap limit, CPU spikes as the collector runs; when the collection completes, CPU briefly drops and memory drops, then the cycle repeats

  * The CPU spike is in the process running the runtime (JVM, Node.js, Python), not in a separate system process

  * `system.cpu.sys` remains low (GC runs in userspace; kernel involvement is minimal)

  * The pattern is bursty and periodic, unlike a runaway loop (which is flat and continuous)




**Correlated signals to check:**

  * `system.mem.used` sawtooth with a rising floor confirms heap pressure alongside CPU

  * Application request latency rising: GC pause times add directly to P99/P999 latency

  * Application error spans: long GC pauses cause downstream timeouts that cascade into errors

  * `system.cpu.user` spikes sync with the sawtooth troughs in `system.mem.used` (each trough is a collection event)




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Know which process is the runtime (JVM, Python, Node.js)| CPU attribution requires identifying the right PID  
GC log file location if the runtime is configured to write one| Provides timestamps and pause durations not visible at OS level  
  
### Steps

  1. **Identify the high-CPU process and confirm it is a runtime**



    
    
    ps aux --sort=-%cpu | head -10
    # Look for: java, python, node, ruby, dotnet
    # Confirm it is a long-running process (TIME column shows cumulative CPU; ELAPSED shows wall clock age)
    
    ps -p <PID> -o pid,cmd,pcpu,pmem,etime

  2. **Confirm CPU correlates with memory sawtooth**



    
    
    # Sample CPU and RSS together every 10 seconds
    PID=<pid>
    while true; do
      cpu=$(ps -p $PID -o pcpu= | tr -d ' ')
      rss=$(awk '/VmRSS/{print $2}' /proc/$PID/status)
      echo "$(date +%T)  CPU=${cpu}%  RSS=${rss} kB"
      sleep 10
    done
    # GC signature: CPU spikes exactly when RSS drops (collection event)
    # Runaway loop signature: CPU is high but RSS is flat

  3. **Check CPU time breakdown (userspace vs kernel)**



    
    
    # Read utime (field 14) and stime (field 15) from /proc/<PID>/stat
    # Take two samples 10 seconds apart and compare delta
    awk '{print "utime=" $14 " stime=" $15}' /proc/<PID>/stat
    sleep 10
    awk '{print "utime=" $14 " stime=" $15}' /proc/<PID>/stat
    # If utime delta >> stime delta: userspace (GC / application), not kernel
    # High stime would suggest I/O or syscall pressure instead

  4. **Check the runtime's startup flags for heap configuration**



    
    
    # JVM: look for -Xmx (max heap), -Xms (initial heap), -Xlog:gc (GC logging)
    cat /proc/<PID>/cmdline | tr '\0' ' '
    
    # Node.js: look for --max-old-space-size
    cat /proc/<PID>/cmdline | tr '\0' ' ' | grep -o 'max-old-space[^ ]*'
    
    # Python: no heap flag; check if process is running with tracemalloc or objgraph

  5. **Look for GC log files on disk**



    
    
    # JVM writes GC logs to a path configured via -Xlog:gc:file= or -Xloggc=
    find /var/log /tmp /opt /home -name "*.log" 2>/dev/null | xargs grep -l "GC\|Heap\|Pause" 2>/dev/null | head -10
    
    # Check cmdline for explicit GC log path
    cat /proc/<PID>/cmdline | tr '\0' '
    ' | grep -iE "gc.*log|log.*gc|Xlog"

  6. **Measure how much of host RAM the process is consuming**



    
    
    cat /proc/<PID>/status | grep -E 'VmRSS|VmSize|VmSwap'
    free -h
    # If VmRSS is > 60-70% of total RAM, the runtime is competing with the OS page cache
    # and other processes for physical memory

  7. **Check for OOM kills (GC pressure can precede OOM if heap exhausts before the next collection)**



    
    
    dmesg | grep -i "out of memory\|killed process"

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Know the current heap / memory configuration of the runtime| Increasing heap without knowing the current setting risks over-allocating  
Confirm the host has available RAM before increasing heap| Giving the runtime more heap than the host can provide triggers OOM kills for other processes  
Coordinate a service restart with the owning team| Increasing heap or changing GC flags requires a restart to take effect  
  
### Immediate Relief

**Restart the process to temporarily relieve GC pressure:**
    
    
    systemctl restart <service-name>
    systemctl status <service-name>

Effective if GC pressure is caused by heap fragmentation or accumulated long-lived objects from the current session. If the root cause is heap too small for current traffic, pressure returns after restart within minutes.

### Increase Available Heap (Runtime-Specific)

First confirm the host has enough free RAM:
    
    
    free -h
    # "available" must exceed the additional heap headroom you plan to allocate

**JVM — increase max heap:**
    
    
    # Find current setting
    cat /proc/<PID>/cmdline | tr '\0' '
    ' | grep -i xmx
    
    # Edit the service start script or systemd unit Environment= line, e.g.:
    #   -Xmx2g  →  -Xmx4g
    # Then restart:
    systemctl restart <service-name>

**Node.js — increase V8 heap:**
    
    
    # Find current setting
    cat /proc/<PID>/cmdline | tr '\0' '
    ' | grep max-old-space
    
    # Edit start command to add or increase, e.g.:
    #   node --max-old-space-size=4096 app.js

**Python — no heap size flag; GC CPU pressure usually indicates a reference cycle accumulation:**
    
    
    # Python GC collects reference cycles; if CPU is high from Python GC,
    # the root cause is likely object retention — treat as a Slow Application Leak

### Set a Memory Limit to Bound Blast Radius
    
    
    # /etc/systemd/system/<service>.service.d/memory-limit.conf
    [Service]
    MemoryMax=6G      # set above expected heap + runtime overhead, below host RAM
    MemorySwapMax=0
    Restart=on-failure
    RestartSec=10s
    
    
    systemctl daemon-reload && systemctl restart <service-name>

### Fix the Underlying Cause

OS-level observations guide the correct application-side fix:

OS observation| Likely cause| Application fix  
---|---|---  
RSS plateaus + high CPU (no RSS growth)| Heap too small for current traffic| Increase `-Xmx` or equivalent  
RSS rising + high CPU (sawtooth floor climbing)| Memory leak forcing frequent GC| Fix the leak; GC pressure is a symptom  
CPU spikes only during traffic surges| Allocation rate exceeds collector throughput under load| Increase heap; review hot allocation paths  
CPU high at night / off-peak| Scheduled job or batch triggering bulk allocation| Investigate cron or maintenance job heap usage  
  
* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Polling `/proc`, `ps`, `vmstat`| None| Read-only  
`systemctl restart <service>`| **Brief service interruption**|  In-flight requests dropped; in-memory state lost  
Increasing heap + restart| **Brief service interruption**|  Same as restart; new heap size takes effect on next JVM/Node.js start  
Setting `MemoryMax` \+ `daemon-reload`| None| Takes effect on next start  
Setting `MemoryMax` \+ restart| **Brief service interruption**|  Same as restart  
**Increasing heap beyond available host RAM**| **Multiple processes OOM-killed**|  Allocating more heap than the host has RAM causes cascading OOM kills across the host; always verify `free -h` first  
  
**Increasing heap beyond available host RAM is the primary risk.** It causes OOM kills across the entire host, not just the target service.

* * *

## Verification
    
    
    # Confirm CPU has recovered
    ps -p <PID> -o pid,pcpu,pmem
    # %CPU should be at normal baseline
    
    # Confirm the sawtooth floor is no longer rising
    PID=<pid>
    while true; do
      rss=$(awk '/VmRSS/{print $2}' /proc/$PID/status)
      cpu=$(ps -p $PID -o pcpu= | tr -d ' ')
      echo "$(date +%T)  RSS=${rss} kB  CPU=${cpu}%"
      sleep 15
    done
    
    # Confirm no OOM kills
    dmesg | grep -i "oom" | tail -5

In Datadog, verify:

  * `system.cpu.user` drops to baseline and remains stable

  * `system.mem.used` sawtooth floor is no longer rising (indicates the heap increase or leak fix is working)

  * Application request latency returns to baseline — GC pauses add directly to tail latency, so P99 recovery confirms the fix

  * If heap was increased: `system.mem.used` settles at a new, higher, stable plateau rather than continuing to climb




* * *

## Related Scenarios

  * If the sawtooth floor keeps rising even after a heap increase, the root cause is a memory leak, not insufficient heap; treat as a Slow Application Leak.

  * If CPU is high but `system.mem.used` shows no sawtooth (RSS is flat), this is not GC pressure; see the Runaway Process / Infinite Loop scenario.

  * GC pressure that only occurs during traffic surges points to allocation rate exceeding collector throughput under load; consider reducing allocation rate at the application level rather than continually increasing heap.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6845071936 -->
