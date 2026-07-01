# Disk Space - Core Dumps

**Signal:** `system.disk.in_use` high; typically a sudden spike rather than a gradual rise  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition or a dedicated `/var/crash` volume

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`ls`| investigation| `ls -lh /var/crash/` · `ls -lh /var/lib/systemd/coredump/`  
`find`| investigation| `find / -name "core.*" 2>/dev/null \| xargs ls -lh`  
`du`| investigation| `du -sh /var/crash/ /var/lib/systemd/coredump/`  
`cat`| investigation| `cat /proc/sys/kernel/core_pattern`  
`coredumpctl`| both| `coredumpctl list` · `coredumpctl info <PID>` · `coredumpctl clean --disk-free 1G`  
`dmesg`| investigation| `dmesg \| grep -i "oom_kill"`  
`journalctl`| investigation| `journalctl -k \| grep -i "killed process"`  
`systemctl`| both| `systemctl status kdump` · `systemctl daemon-reexec`  
`rm`| remediation| `rm -f /var/crash/*` · `rm -f /var/lib/systemd/coredump/*`  
  
* * *

## What Happens

When a process crashes, the kernel or a crash reporter writes a core dump (a snapshot of the process memory at the time of failure) to disk. A single dump can range from hundreds of MB to tens of GB depending on the process's memory footprint. Repeated crashes from the same service fill the disk rapidly and can themselves trigger further crashes (processes that try to write to a full disk also fail).

Common triggers:

  * An application has a memory bug (segfault, stack overflow) and crashes in a loop

  * An OOM killer event caused a process to be killed, and the crash reporter wrote a dump before clean exit

  * Kernel crash dump (`kdump`) was triggered by a kernel panic and wrote a vmcore file

  * The crash dump destination (`/var/crash`, `core_pattern`) is on the same partition as application data or logs

  * Core dump file size limits were never set (`ulimit -c unlimited` in a startup script)

  * `systemd-coredump` is enabled and accumulating dumps in `/var/lib/systemd/coredump/`




* * *

## Detection

The platform detects this via `system.disk.in_use` breaching the monitor threshold. Because crashes produce sudden large files, the metric often shows a sharp step-function rise rather than a gradual trend. This distinguishes it from log accumulation (which trends upward) and database growth (which is slow and steady).

**Correlated signals to check:**

  * Elevated error span or error log counts at the time of the disk spike (the crashing application was generating errors before it crashed)

  * Application monitor going to ALERT or NO_DATA around the same time (the process died)

  * `system.mem.page_faults` spikes preceding the crash event




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Required to inspect the filesystem  
Root or `adm` group access| Core dumps under `/var/crash` and `/var/lib/systemd/coredump/` typically require root to read  
`find`, `du`, `ls`, `dmesg` available on the host| Standard on all Linux distributions  
Kernel crash dump analysis tools (`crash`, `kdump`) if a kernel panic is suspected| Needed to inspect vmcore files  
  
### Steps

  1. **Locate core dump files**



    
    
    # Common locations
    ls -lh /var/crash/
    ls -lh /var/lib/systemd/coredump/
    ls -lh /tmp/core* /tmp/*.core 2>/dev/null
    
    # Search the whole filesystem for core files (slow on large disks)
    find / -name "core" -o -name "core.*" -o -name "*.core" 2>/dev/null | xargs ls -lh 2>/dev/null
    
    # Check kernel core_pattern to know where dumps land
    cat /proc/sys/kernel/core_pattern
    

  2. **Identify the crashing process**



    
    
    # systemd-coredump provides structured metadata
    coredumpctl list
    # Shows: TIME, PID, UID, GID, SIG, COREFILE, EXE
    
    coredumpctl info <PID>
    # Shows: signal received, backtrace (if symbols available), executable path
    

  3. **Check for OOM kills**



    
    
    dmesg | grep -i "out of memory"
    dmesg | grep -i "oom_kill"
    # Look for: "Out of memory: Kill process <PID> (<name>) score <N>"
    
    journalctl -k --since "1 hour ago" | grep -i "killed process"
    

  4. **Check for kernel panics**



    
    
    ls -lh /var/crash/
    # vmcore files from kdump are typically > 1 GB
    
    # Check if kdump service is active
    systemctl status kdump 2>/dev/null || systemctl status crash 2>/dev/null
    

  5. **Check crash frequency**



    
    
    # How many times has this process crashed recently?
    coredumpctl list | grep <exe-name>
    
    # Or check application process supervisor logs
    journalctl -u <service-name> --since "24 hours ago" | grep -i "exit\|crash\|signal\|killed"
    

  6. **Estimate total disk consumed by dumps**



    
    
    du -sh /var/crash/ /var/lib/systemd/coredump/ /tmp/core* 2>/dev/null
    

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Core dumps must have been analyzed (or explicitly waived) before deletion| Dumps are the primary forensic artifact for diagnosing the crash; deleting them before analysis destroys the evidence  
Root access| Required to delete files in `/var/crash` and coredump directories  
The underlying crash must be addressed separately| Disk space is recovered by deleting dumps, but the crash will recur unless the root cause is fixed  
If the crashing service owns critical data, coordinate with the owning team| Some services need controlled restart; do not assume it is safe to restart any service  
  
### Immediate Space Recovery

**Delete analyzed or waived core dump files:**
    
    
    # Delete all files in crash directory
    rm -f /var/crash/*
    
    # Delete systemd-coredump archives
    rm -f /var/lib/systemd/coredump/*
    
    # Or vacuum via coredumpctl (keeps N most recent)
    coredumpctl clean --disk-free 1G
    # Removes oldest dumps until 1 GB is free
    

**Delete kernel vmcore files (kdump):**
    
    
    # Only after analysis or explicit waiver from the kernel/SRE team
    rm -f /var/crash/vmcore*
    

### Prevent Accumulation

**Disable core dumps for a service (prevents new dumps while the crash root cause is being fixed):**
    
    
    # /etc/security/limits.conf or /etc/security/limits.d/<service>.conf
    <service-user>  soft  core  0
    <service-user>  hard  core  0
    

**Disable core dumps globally via systemd:**
    
    
    # /etc/systemd/system.conf or /etc/systemd/user.conf
    DefaultLimitCORE=0
    

Apply with `systemctl daemon-reexec` (does not restart running services).

**Cap systemd-coredump storage:**
    
    
    # /etc/systemd/coredump.conf
    [Coredump]
    Storage=external
    Compress=yes
    ProcessSizeMax=2G
    ExternalSizeMax=2G
    MaxUse=10G
    KeepFree=5G
    

Apply with `systemctl restart systemd-coredump.socket`.

### Fix the Underlying Crash

This is the only permanent solution. Steps depend on the crash type:

Crash type| Next step  
---|---  
Segfault / SIGSEGV| Analyze the backtrace from `coredumpctl info`; identify the faulting code path; deploy a fix  
OOM kill| Increase memory limits for the container/service, or fix a memory leak; check `system.mem.used` trend  
Recurring application panic| Review application error logs just before the crash; correlate with a recent deploy  
Kernel panic (vmcore)| Escalate to kernel/infra team; analyze with `crash` tool  
  
* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Deleting core dump files| None| Dumps are post-mortem artifacts; the processes that wrote them are already dead  
Deleting vmcore (kernel crash dump)| None| The kernel has already recovered or the host has already rebooted  
`coredumpctl clean`| None| Only removes old dump archives  
Setting `DefaultLimitCORE=0` \+ `systemctl daemon-reexec`| None for running services| `daemon-reexec` re-executes the systemd manager but does not restart managed services; the limit applies to newly started services only  
Restarting `systemd-coredump.socket`| None| Only the dump handler restarts; application services continue  
**Fixing the crashing application**| **Service restart required**|  Deploying a bug fix typically requires restarting the affected service; coordinate for a rolling restart or maintenance window  
**OOM fix via memory limit increase**| **Service restart required**|  Memory limits (cgroup, systemd `MemoryMax`, Kubernetes resource limits) require a service or pod restart to take effect  
**Host reboot** (only if kernel panic fix requires it)| **Full host outage**|  All services on the host go down; schedule a maintenance window and ensure workloads can failover  
  
**The crash itself is the primary service disruption.** By the time remediation starts, the affected process has already died. Disk cleanup has no additional service impact. The service restart to deploy a fix is the only planned disruption.

* * *

## Verification
    
    
    # Confirm dump files are removed
    ls -lh /var/crash/ /var/lib/systemd/coredump/
    
    # Confirm disk space recovered
    df -h <device>
    
    # Confirm the crashing service is healthy
    systemctl status <service-name>
    coredumpctl list --since "1 hour ago"   # should show no new entries
    

In Datadog, verify:

  * `system.disk.in_use` drops below the warning threshold

  * The application's error span/log count returns to baseline

  * The service monitor returns to OK state




* * *

## Related Scenarios

  * If the crash was caused by OOM, memory pressure will also show up in the health score via `mem_used_ratio`; check `system.mem.used` and `system.mem.total`.

  * If the host is filling up repeatedly from new dumps despite setting limits, the crash loop is ongoing and the service itself needs to be stopped until the fix is deployed.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6546722498 -->
