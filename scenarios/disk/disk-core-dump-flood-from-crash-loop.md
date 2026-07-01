# Disk - Core Dump Flood from Crash Loop

**Signal:** Rapid, continuous growth in `/var/crash` or `/var/lib/systemd/coredump/`; `system.disk.in_use` rising by GBs per minute; service monitor simultaneously in ALERT or NO_DATA  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition or a dedicated `/var/crash` volume

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`systemctl`| both| `systemctl status <service>` · `systemctl stop <service>` · `systemctl daemon-reexec`  
`journalctl`| investigation| `journalctl -u <service> -n 50 --no-pager` · `journalctl -u <service> -f`  
`coredumpctl`| both| `coredumpctl list` · `coredumpctl clean --disk-free 5G`  
`sysctl`| remediation| `sysctl -w kernel.core_pattern=\|/bin/false`  
`ls`| investigation| `ls -lhtr /var/crash/ \| tail -20` · `ls -lhtr /var/lib/systemd/coredump/ \| tail -20`  
`du`| investigation| `du -sh /var/crash/ /var/lib/systemd/coredump/`  
`rm`| remediation| `rm -f /var/crash/*`  
`find`| investigation| `find /var/crash /var/lib/systemd/coredump -mmin -10 -ls`  
`cat`| investigation| `cat /proc/sys/kernel/core_pattern`  
  
* * *

## What Happens

This scenario combines two simultaneous failures: a service is crash-looping AND the resulting dump files are filling the disk. Neither resolves on its own.

**The crash loop mechanism** : a service supervisor (systemd `Restart=on-failure`, Kubernetes `restartPolicy: Always`, or a custom watchdog) detects the crash and restarts the service. The service crashes again immediately, often within seconds, and the supervisor restarts it again. This continues indefinitely unless the supervisor hits a restart limit or is manually stopped.

**The dump flood mechanism** : each crash writes a new core dump. If the process has a 1 GB RSS footprint and crashes every 60 seconds, `/var/crash` fills at roughly 1 GB per minute. With a 50 GB `/var/crash` partition, the disk is full in under an hour.

**Why it is worse than a simple crash** : once the disk fills, the service cannot be recovered even if the bug is fixed — the patch deployment fails (`no space left on device`), and any process on the host that tries to write (log files, metrics, other services) may also fail. A single crashing service can take down an otherwise healthy host.

**Common causes** :

  * A bad deploy introduced a crash bug that triggers immediately on startup (the service never reaches a healthy state, so the supervisor keeps restarting it)

  * An OOM condition on every start: the service is allocated insufficient memory and the OOM killer terminates it on every boot

  * A configuration error that causes a panic on init (missing required env var, malformed config file, missing secret mount)

  * An external dependency that the service cannot handle being absent (it crashes rather than degrading gracefully)

  * `Restart=always` with no `StartLimitBurst` or `StartLimitIntervalSec` cap, or a crash that resets the restart counter on each cycle




* * *

## Detection

The platform detects this via `system.disk.in_use` threshold. The distinguishing feature versus a single crash is the **growth rate** : a crash loop produces a staircase pattern on `system.disk.in_use` where each step corresponds to one dump. If the steps are spaced seconds to minutes apart, a loop is active.

**Look for simultaneous signals** :

  * Service monitor in ALERT or NO_DATA at the same time as the disk alert — confirms the crash is active, not historical

  * `process.run_time.sum` or `process.cpu.normalized.pct` showing a process repeatedly appearing and disappearing

  * High restart count in `kubernetes.containers.restarts` (Kubernetes) or systemd `NRestarts` counter




**Estimate time-to-full from the crash rate** :
    
    
    # Estimate: crash interval ≈ time between most-recent dump files
    ls -lhtr /var/lib/systemd/coredump/ | tail -5
    # If 5 dumps appeared in the last 2 minutes: crash rate ≈ 30s/crash
    # If process RSS is ~2 GB: disk fills at ~4 GB/min

* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| All commands run locally  
Root or `admin` group access| Dump directories require elevated access  
Know the service name| Needed to find the crashing process and stop the supervisor  
  
### Steps

  1. **Confirm a loop is active (not historical)**



    
    
    # Check if new dumps are still appearing
    watch -n5 'ls -lhtr /var/lib/systemd/coredump/ | tail -5'
    # If the listing changes every few seconds, the loop is live
    
    # Or count dumps created in the last 10 minutes
    find /var/crash /var/lib/systemd/coredump -mmin -10 -ls | wc -l

  2. **Identify the crashing service**



    
    
    # systemd-coredump: shows crash history with timestamp, PID, executable
    coredumpctl list | tail -20
    
    # Check which service has been restarting
    systemctl --state=failed
    journalctl -xe --since "10 minutes ago" | grep -i "start request\|failed\|crash\|signal"

  3. **Check crash rate and restart count**



    
    
    systemctl status <service>
    # Look for: "Main PID", "Active: activating (start) ...", "NRestarts: 47"
    # A high NRestarts or rapidly changing status confirms a loop
    
    journalctl -u <service> -n 30 --no-pager
    # Look for alternating "Started" / "Failed" / "Scheduled restart" lines

  4. **Check current disk headroom**



    
    
    du -sh /var/crash/ /var/lib/systemd/coredump/
    df -h /var/crash   # or df -h / if crash dir is on root
    # Estimate minutes until full: remaining_space / (dump_size * crash_rate)

  5. **Verify where dumps are being written**



    
    
    cat /proc/sys/kernel/core_pattern
    # Common values:
    #   |/usr/lib/systemd/systemd-coredump ... → goes to systemd-coredump
    #   /var/crash/core.%e.%p.%t            → goes to /var/crash
    #   /dev/null                           → dumps disabled (nothing to clean)

* * *

## Remediation

**Order matters** : stop the loop before cleaning. If you delete dumps while the service is still crash-looping, new dumps refill the space within minutes.

### Preconditions

Precondition| Rationale  
---|---  
Notify the owning team before stopping the service| The service is already down, but stopping the supervisor ends any in-progress recovery attempts and may affect SLA calculations or on-call escalation  
Preserve at least one dump for analysis| One dump is sufficient to diagnose the crash; keep the most recent before deleting the rest  
Coordinate if multiple services share the partition| Disk pressure may be affecting other services; communicate before taking actions that change host-level config  
  
### Step 1: Stop the crash loop
    
    
    # Stop the service and prevent automatic restart
    systemctl stop <service>
    
    # If systemd keeps restarting it despite stop, mask it temporarily
    systemctl mask <service>
    # Undo later with: systemctl unmask <service>

For Kubernetes: scale the deployment to 0 replicas, or cordon the node and drain to stop further scheduling there.

### Step 2: Disable dump generation (prevents new dumps if the loop resumes)

**Immediate, non-persistent (survives until reboot only):**
    
    
    # Redirect all core dumps to /dev/null
    sysctl -w kernel.core_pattern='|/bin/false'
    # Verify:
    cat /proc/sys/kernel/core_pattern

**Persistent, per-service via systemd:**
    
    
    # Override file: /etc/systemd/system/<service>.d/no-coredump.conf
    [Service]
    LimitCORE=0

Apply with `systemctl daemon-reload && systemctl restart <service>` (after the loop is fixed).

**Persistent, system-wide via systemd:**
    
    
    # /etc/systemd/system.conf
    DefaultLimitCORE=0

Apply with `systemctl daemon-reexec` (does not restart running services).

### Step 3: Recover disk space
    
    
    # Keep the most recent dump for analysis; delete the rest
    ls -t /var/lib/systemd/coredump/ | tail -n +2 | xargs -I{} rm /var/lib/systemd/coredump/{}
    
    # Or use coredumpctl to leave a target free amount
    coredumpctl clean --disk-free 5G
    
    # Or delete everything if analysis is waived / already done
    rm -f /var/crash/*
    rm -f /var/lib/systemd/coredump/*

### Step 4: Fix the underlying crash

This is the only permanent resolution. One dump is enough to identify the crash type:
    
    
    # Get the backtrace from the most recent dump
    coredumpctl info   # most recent entry
    # Look for: signal received, executable, backtrace lines

Crash type| Next step  
---|---  
SIGSEGV / SIGABRT with backtrace| Identify the faulting frame; correlate with recent deploy; roll back or hotfix  
OOM kill (signal 9 from kernel)| The process is being killed by the OOM killer on every start — raise its memory limit or reduce footprint before restarting  
SIGTERM / clean exit + supervisor restart| The service is exiting cleanly but the supervisor treats any non-zero exit as a crash — check exit codes and supervisor restart policy  
Panic on init (config / dependency)| Service cannot start due to a missing config value or unreachable dependency — fix config, restore the dependency, or add a startup health check  
  
### Step 5: Re-enable dumps and restart

Once the fix is deployed:
    
    
    # Re-enable dump generation (revert sysctl)
    sysctl -w kernel.core_pattern="|/usr/lib/systemd/systemd-coredump %P %u %g %s %t %c %h"
    # or whatever the original pattern was (check /etc/sysctl.d/ for the configured value)
    
    # Unmask and start the service
    systemctl unmask <service>
    systemctl start <service>

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Inspecting dumps, `coredumpctl list`| None| Read-only  
`systemctl stop <service>`| **Service remains down** (it was already down)| Ends the crash loop; no additional disruption beyond what the crash already caused  
`systemctl mask <service>`| **Prevents recovery until unmasked**|  Use only if `stop` is insufficient to halt the loop; remember to unmask before deploying the fix  
`sysctl -w kernel.core_pattern=...`| None| Host-wide but affects only future crashes; no impact on running processes  
`rm -f /var/crash/*`| None| Post-mortem artifacts only; deleting dumps has no runtime effect  
`coredumpctl clean`| None| Same as above  
`systemctl daemon-reexec`| None for running services| Re-executes the systemd manager binary; does not restart managed services  
**Fixing the crash and restarting the service**|  Service comes back up — brief restart gap| This is the desired outcome  
  
**The primary risk is unmasking or restarting the service before the fix is deployed.** If the service is restarted while still crash-prone, the loop resumes and the disk refills. Confirm the fix is in place before step 5.

* * *

## Verification
    
    
    # Confirm no new dumps are appearing
    watch -n10 'ls -lhtr /var/lib/systemd/coredump/ | tail -5'
    # Should be static
    
    # Confirm disk recovered
    df -h /var/crash   # or df -h /
    
    # Confirm service is stable
    systemctl status <service>
    journalctl -u <service> -n 20 --no-pager
    # Should show "Active: active (running)" with no recent restart events
    
    # Confirm NRestarts is no longer climbing
    systemctl show <service> --property=NRestarts

In Datadog, verify:

  * `system.disk.in_use` levels off and then drops as the dump directory is cleaned

  * Service monitor returns to OK state and stays there (no flapping)

  * No new `no space left on device` errors in application or system logs




* * *

## Related Scenarios

  * If the crash was triggered by OOM, the root cause is memory pressure — see Memory: Slow Application Leak or Memory: Unbounded Cache / Session Growth for investigation of the memory growth that preceded the kill.

  * If other services on the same host began failing at the same time (writes rejected, log files not rotating), the disk filled enough to affect them — treat as a host-level incident, not just a single-service issue.

  * For a single crash (no loop), see [Disk Space Issues Due to Core Dumps and Crash Management](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6546722498/Disk+Space+Issues+Due+to+Core+Dumps+and+Crash+Management>) for detailed forensic analysis steps and dump retention configuration.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6866043104 -->
