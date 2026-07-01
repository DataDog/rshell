# Disk Space - Unrotated / Unbounded Logs

**Signal:** `system.disk.in_use` rising steadily; log directory growth rate correlates with application activity  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition or a dedicated `/var/log` volume

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`df`| investigation| `df -h`  
`du`| investigation| `du -h --max-depth=2 /var/log \| sort -rh \| head -20`  
`ls`| investigation| `ls -lhS /var/log/<service>/`  
`find`| investigation| `find /var/log -name "*.log" -size +500M` · `find /var/log -mtime -1 -name "*.log"`  
`logrotate`| both| `logrotate -d /etc/logrotate.d/<service>` · `logrotate -f /etc/logrotate.conf`  
`cat`| investigation| `cat /etc/logrotate.d/<service>`  
`lsof`| investigation| `lsof \| grep deleted \| grep log`  
`journalctl`| both| `journalctl --disk-usage` · `journalctl --vacuum-size=500M` · `journalctl --vacuum-time=7d`  
`systemctl`| investigation| `systemctl status logrotate.timer` · `systemctl list-timers \| grep logrotate`  
`truncate`| remediation| `truncate -s 0 /var/log/<service>/<file>.log`  
`rm`| remediation| `rm -f /var/log/<service>/*.log.<N>`  
  
* * *

## What Happens

Log files grow as applications write to them. Without rotation or retention limits, a log file grows indefinitely for the lifetime of the service. Unlike core dumps (sudden large files) or database growth (slow data accumulation), log growth is typically steady and proportional to application activity — accelerating during high-traffic periods or error storms and decelerating overnight.

Common causes:

  * A service has no logrotate configuration — logs have never been rotated since the service was deployed

  * logrotate is configured but the `rotate` count is too high or `maxsize` / `size` thresholds are never triggered

  * A log storm: an application bug or error loop writes error lines at high rate, growing the log file orders of magnitude faster than normal

  * The logrotate timer or cron job is broken or was never enabled (`systemctl status logrotate.timer`)

  * Old rotated log files (`*.log.1`, `*.log.2.gz`) are never deleted because the `rotate N` count is set too high or missing

  * A process holds the deleted log file open after rotation: the inode stays allocated and disk space is not reclaimed until the process is restarted or sends SIGHUP to re-open the file

  * `journald` is configured with no size limit and accumulates logs from all systemd services indefinitely




* * *

## Detection

The platform detects this via `system.disk.in_use` breaching the monitor threshold. The defining characteristic is a steady, linear rise over hours or days — proportional to application log output. This distinguishes it from core dumps (sudden step-function spike) and database growth (very slow, data-volume-driven).

**Correlated signals to check:**

  * `system.disk.in_use` growth rate matches known application traffic patterns (higher during business hours, flat overnight) — confirms log growth rather than another source

  * Application error span count elevated: a log storm is often preceded or accompanied by a spike in error logs

  * Check `system.disk.in_use` on `/var/log` specifically if it is a separate volume

  * `journalctl --disk-usage` growing beyond expected bounds on systemd hosts




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Read access to `/var/log` and logrotate config directories| Required to inspect log sizes and rotation config  
Ability to run `lsof`| Requires root or equivalent to list all open file descriptors  
  
### Steps

  1. **Identify which log directory is consuming the most space**



    
    
    du -h --max-depth=2 /var/log 2>/dev/null | sort -rh | head -20
    # Focus on directories with unexpected sizes or that are growing

  2. **Find the largest individual log files**



    
    
    find /var/log -name "*.log" -o -name "*.log.*" 2>/dev/null | xargs ls -lhS 2>/dev/null | head -20
    # Large uncompressed rotated files (*.log.1, *.log.2) indicate rotation without cleanup
    # A single huge *.log file indicates no rotation at all

  3. **Check how fast the active log file is growing**



    
    
    # Sample size twice, 60 seconds apart
    LOG=/var/log/<service>/<file>.log
    size1=$(stat -c%s "$LOG" 2>/dev/null); sleep 60; size2=$(stat -c%s "$LOG" 2>/dev/null)
    echo "Growth: $((size2 - size1)) bytes/min"
    # High growth rate = active log storm; low rate = accumulation over time without rotation

  4. **Check the logrotate configuration for the service**



    
    
    cat /etc/logrotate.d/<service>
    # Key fields to verify:
    #   rotate N       — how many old files to keep (missing = keep forever)
    #   size / maxsize — trigger rotation at this file size (missing = only time-based)
    #   daily/weekly   — rotation frequency
    #   compress       — whether old files are gzip'd
    #   postrotate     — script to signal the process to re-open log files
    
    # Dry-run logrotate to see what it would do
    logrotate -d /etc/logrotate.d/<service>

  5. **Check if the logrotate timer is running**



    
    
    systemctl status logrotate.timer
    systemctl list-timers | grep logrotate
    # Confirm last trigger time and next scheduled run
    # If inactive or failed: logrotate has not run recently

  6. **Check for deleted-but-open log files (space not reclaimed after rotation)**



    
    
    lsof | grep deleted | grep -i log
    # Output: COMMAND PID USER FD SIZE NAME (deleted)
    # If the active log path shows as "(deleted)", the process is still writing to the old inode
    # Space is not reclaimed until the process is restarted or re-opens the file

  7. **Check journald disk usage**



    
    
    journalctl --disk-usage
    # Shows total space used by the systemd journal
    # Compare against /etc/systemd/journald.conf SystemMaxUse= setting
    cat /etc/systemd/journald.conf | grep -E "SystemMaxUse|RuntimeMaxUse|MaxRetentionSec"

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Confirm the log files are not needed for an active incident or audit| Deleting or truncating logs destroys forensic evidence; check with the owning team if an incident is open  
Identify whether a log storm is ongoing before truncating| If the application is actively in an error loop, truncating the log recovers space temporarily but the file regrows immediately; fix the root cause first  
Confirm the process handles SIGHUP for log re-open (if using copytruncate-less rotation)| Some processes require SIGHUP or a restart to re-open the log file after rotation  
  
### Immediate Space Recovery

**Delete old rotated log files:**
    
    
    # List rotated files for the service first
    ls -lh /var/log/<service>/*.log.* /var/log/<service>/*.gz 2>/dev/null
    
    # Delete all rotated (non-active) log files
    rm -f /var/log/<service>/*.log.[0-9]*
    rm -f /var/log/<service>/*.log.*.gz

**Truncate the active log file (preserves the file and inode; process keeps writing):**
    
    
    # Safe: truncate in-place — the process's file descriptor stays valid
    truncate -s 0 /var/log/<service>/<file>.log
    
    # Alternative using shell redirection
    > /var/log/<service>/<file>.log

**Force a logrotate run immediately:**
    
    
    logrotate -f /etc/logrotate.d/<service>
    # -f forces rotation even if the size/time threshold has not been met

**Reclaim space from deleted-but-open log files:**
    
    
    # Signal the process to re-open its log file (if it supports SIGHUP)
    kill -HUP <PID>
    
    # Verify the deleted file entry is gone
    lsof | grep deleted | grep -i log
    
    # If SIGHUP is not supported, a service restart is required
    systemctl restart <service-name>

**Vacuum journald:**
    
    
    # Keep only the last 500 MB
    journalctl --vacuum-size=500M
    
    # Or keep only the last 7 days
    journalctl --vacuum-time=7d

### Fix the Underlying Configuration

**Add or fix a logrotate config for the service:**
    
    
    # /etc/logrotate.d/<service>
    /var/log/<service>/*.log {
        daily
        rotate 7
        compress
        delaycompress
        missingok
        notifempty
        maxsize 500M
        postrotate
            systemctl reload <service> 2>/dev/null || true
        endscript
    }

Key settings:

Setting| Purpose  
---|---  
`rotate 7`| Keep at most 7 old log files; delete older ones automatically  
`maxsize 500M`| Rotate immediately if the file exceeds 500 MB, regardless of schedule  
`compress` / `delaycompress`| Gzip old logs; delay one cycle so the previous file is not compressed while still potentially in use  
`postrotate`| Reload or signal the service to re-open the new log file after rotation  
  
**Cap journald size permanently:**
    
    
    # /etc/systemd/journald.conf
    [Journal]
    SystemMaxUse=2G
    MaxRetentionSec=1month

Apply with:
    
    
    systemctl restart systemd-journald

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`du`, `find`, `ls`, `lsof`, `logrotate -d`| None| Read-only inspection  
`rm` on old rotated log files| None| Rotated files are no longer written to by the application  
`truncate -s 0` on the active log file| None| The process's file descriptor remains valid; it continues writing from offset 0  
`logrotate -f`| None for most services| The postrotate script may send SIGHUP; confirm the service handles it gracefully  
`kill -HUP <PID>`| None for most services| Causes the process to re-open log files; may briefly flush buffers  
`journalctl --vacuum-*`| None| Only removes old journal data; no running processes affected  
`systemctl restart systemd-journald`| **Brief gap in log collection**|  Journal entries from all services are buffered and may be lost during the restart window  
`systemctl restart <service>` (to release deleted fd)| **Brief service interruption**|  Required only if the process does not support SIGHUP for log re-open  
  
**Truncating an active log file is always safe.** The process keeps its file descriptor; writes continue to the now-empty file. No restart is needed and no data is lost from the running process.

* * *

## Verification
    
    
    # Confirm log directory has shrunk
    du -sh /var/log/<service>/
    
    # Confirm disk space recovered
    df -h <device>
    
    # Confirm no deleted-but-open log files remain
    lsof | grep deleted | grep -i log
    
    # Confirm logrotate timer is active and will run on schedule
    systemctl list-timers | grep logrotate

In Datadog, verify:

  * `system.disk.in_use` drops after cleanup and the growth rate returns to a sustainable slope (or flat if the storm is resolved)

  * If a log storm was the cause: application error span count returns to baseline, confirming the underlying issue has been fixed




* * *

## Related Scenarios

  * If the log file is growing at an abnormal rate (gigabytes per hour rather than per day), an application is in an error loop; fix the root cause before truncating — the file will regrow immediately otherwise.

  * If `lsof | grep deleted` shows large deleted-but-open files and a service restart is not immediately possible, the space cannot be reclaimed until the process releases the file handle; quantify the size and plan a maintenance window.

  * If `/var/log` is on the root partition and is nearly full, other writes (application temp files, package manager operations) will also start failing; prioritize recovery before the partition reaches 100%.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6851068627 -->
