# Disk - Inode Exhaustion from Small File Accumulation

**Signal:** `system.fs.inodes.in_use` at or near 1.0 per device (Datadog Agent); `system.filesystem.inodes.usage{state=used}` approaching max (OpenTelemetry host metrics); confirmed on the host with `df -i` showing IUse% at 100% while `df -h` shows significant free space; processes failing with `No space left on device` or `ENOSPC` despite `system.disk.in_use` appearing normal  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition, `/tmp`, `/var`, or any partition hosting a workload that creates many small files

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`df`| investigation| `df -i` · `df -ih`  
`find`| both| `find /tmp -maxdepth 3 -type f \| wc -l` · `find / -xdev -maxdepth 6 -printf '%h  
| sort | uniq -c | sort -rn | head -20` || |   
`ls`| investigation| `ls \| wc -l` · `ls -la /tmp \| wc -l`  
`du`| investigation| `du --inodes -d 3 /var \| sort -rn \| head -20`  
`stat`| investigation| `stat -f /var/spool/`  
`rm`| remediation| `find /tmp -maxdepth 1 -type f -mtime +1 -delete`  
`tune2fs`| investigation| `tune2fs -l /dev/sda1 \| grep -i inode`  
  
* * *

## What Happens

Every file on a Unix filesystem, regardless of its size, occupies exactly one **inode** — a metadata entry that records the file's owner, permissions, timestamps, and pointer to its data blocks. The number of inodes on a filesystem is fixed at creation time (ext4 allocates roughly one inode per 16 KB of storage by default). Once all inodes are consumed, no new files can be created, even if gigabytes of data blocks are still free.

This is the core confusion: the error is identical to a full disk (`No space left on device` / `ENOSPC`), but `system.disk.in_use` and byte-level dashboards look completely normal.

**Why it is hard to notice in advance** : `system.disk.in_use` tracks bytes, not inodes. `system.fs.inodes.in_use` (Datadog) and `system.filesystem.inodes.usage` (OTel) are the correct metrics, but they are not included in most default disk monitors. Inode exhaustion can accumulate invisibly for weeks — the inode counter climbs toward 1.0 while byte usage stays flat — and only becomes visible the moment the last inode is consumed.

**Common workloads that exhaust inodes** :

Workload| Mechanism  
---|---  
PHP session files| Each HTTP session creates one file in `/var/lib/php/sessions/`; sessions expire slowly; a busy site accumulates millions  
Mail queue| Each queued message is a file; a stuck queue or spam run fills inodes quickly  
Build artifacts| `node_modules/` trees, Java `.class` files, Python `__pycache__`/`.pyc`, Gradle caches — each package or class is a separate file  
Container overlay layers| Each container layer creates many small metadata files under `/var/lib/docker/overlay2/`  
Log rotation fragments| Aggressive rotation with short `rotate` counts splits one log stream into thousands of small files  
Metrics/cache files| Prometheus TSDB, StatsD, or similar tools writing one file per metric series  
`/tmp` temp files from crashed processes| Processes that crash after creating temp files but before deleting them leave orphaned files indefinitely  
  
* * *

## Detection

The platform may not alert on this scenario unless inode monitoring is explicitly configured. If you receive an alert, it likely came from one of:

  * A monitor on `system.fs.inodes.in_use` (Datadog) or `system.filesystem.inodes.usage` (OTel) — if configured

  * Application error log monitors catching repeated `ENOSPC` or `Too many open files` errors

  * An on-call alert from a failed deployment or failed write operation




**First indication is usually an application error, not a disk alert.** Common symptoms:

  * Web servers returning 500 errors; application logs showing `failed to create temp file`, `cannot create socket`, or `ENOSPC`

  * `cron` jobs silently failing (cron cannot write its lock file)

  * Package managers (`apt`, `yum`) failing to install or update packages

  * New files cannot be created but existing files can still be read and written to

  * `df -h` looks normal; engineer spends time investigating "disk full" on what appears to be a healthy host




**Confirming inode exhaustion** :
    
    
    df -i
    # Filesystem       Inodes   IUsed   IFree IUse%  Mounted on
    # /dev/sda1       3932160 3932160       0  100%  /
    # /dev/sdb1       1048576  800000  248576   76%  /data
    #
    # IUse% at 100% on / confirms the issue

* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| All commands run locally  
Root or sudo access| Some directories require elevated access to read file counts  
Time: finding the culprit directory can take minutes| `find` across large filesystems is slow; use `-xdev` to stay on one partition and `-maxdepth` to limit scope; start from likely suspects (`/tmp`, `/var`, application data dirs) before doing a full scan  
  
### Steps

  1. **Confirm inode exhaustion and identify the affected partition**



    
    
    df -i
    # IUse% at or near 100% on one or more partitions
    
    df -ih   # same output with human-readable inode counts

  2. **Find the directory with the most files — fast approach (start with suspects)**



    
    
    # Check common culprits first — each takes seconds
    ls /tmp | wc -l
    ls /var/spool/mail/ | wc -l
    ls /var/lib/php/sessions/ 2>/dev/null | wc -l
    ls /var/spool/postfix/deferred/ 2>/dev/null | wc -l
    find /var/tmp -maxdepth 2 -type f | wc -l

  3. **Inode usage by directory (du --inodes, fast and safe)**



    
    
    # Shows which directories consume the most inodes; -d 3 limits depth
    du --inodes -d 3 /var 2>/dev/null | sort -rn | head -20
    du --inodes -d 3 /tmp 2>/dev/null | sort -rn | head -20

  4. **Full filesystem scan (slower; use when suspects above yield nothing)**



    
    
    # Print the parent directory of every file, count, sort — stays on one filesystem with -xdev
    find / -xdev -maxdepth 8 -printf '%h
    ' 2>/dev/null \
      | sort | uniq -c | sort -rn | head -20
    # Output: count  directory — the top line is the culprit

  5. **Inspect the culprit directory**



    
    
    # Once you know the directory (e.g. /var/lib/php/sessions):
    ls /var/lib/php/sessions | wc -l     # total file count
    ls -lt /var/lib/php/sessions | head  # most recently modified
    ls -lut /var/lib/php/sessions | tail # oldest (least recently accessed)
    
    # Check the age distribution
    find /var/lib/php/sessions -mtime +1 | wc -l   # older than 1 day
    find /var/lib/php/sessions -mtime +7 | wc -l   # older than 7 days

  6. **Check filesystem inode limits**



    
    
    # How many inodes does the filesystem have total?
    tune2fs -l /dev/sda1 | grep -i inode
    # Inode count: 3932160
    # Free inodes: 0
    
    stat -f /var/spool/
    # Inodes: 3932160  Free Inodes: 0

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Identify the file type before deleting| Session files, mail queue files, and cache files may contain active user data or undelivered messages; confirm with the owning team before bulk deletion  
Do not delete files that are open by running processes| Files with active file descriptors should be truncated (not deleted) if the process must keep running; use `lsof` to check before bulk `rm`  
Test deletion on a small batch first| On a directory with millions of files, `rm -rf *` can itself fail (argument list too long); use `find ... -delete` instead  
  
### Immediate Relief

**Session files (PHP, Ruby, etc.) — safe to delete files older than the session TTL:**
    
    
    # Delete PHP session files not accessed in more than 24 hours
    find /var/lib/php/sessions -maxdepth 1 -type f -atime +1 -delete
    
    # If even find is slow (millions of files), use xargs in batches:
    find /var/lib/php/sessions -maxdepth 1 -type f -atime +1 \
      | xargs -P4 -n1000 rm -f

**Temp files — files older than 1 day in**`/tmp` are generally safe:
    
    
    find /tmp -maxdepth 2 -type f -mtime +1 -delete
    find /var/tmp -maxdepth 2 -type f -mtime +1 -delete

**Build artifacts (node_modules, .pyc, .class) — check with the owning team:**
    
    
    # Example: delete Python bytecode cache
    find /app -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null
    
    # Example: delete node_modules from old build directories
    find /builds -maxdepth 3 -name 'node_modules' -type d -mtime +7 -exec rm -rf {} +

**Mail queue (postfix) — only after confirming messages are safe to drop:**
    
    
    # List queue depth
    postqueue -p | tail -1
    
    # Flush or delete deferred messages (coordinate with the mail team)
    postsuper -d ALL deferred

### Prevent Recurrence

**Monitor inodes explicitly** — add a Datadog monitor on `system.fs.inodes.in_use`:
    
    
    # Metric: system.fs.inodes.in_use
    # Alert threshold: > 0.80 (80%)
    # Group by: host, device
    # This gives early warning before the 1.0 hard wall is hit

For OTel pipelines: alert on `system.filesystem.inodes.usage{state=used}` as a fraction of `system.filesystem.inodes.usage` total.

**Configure application session / temp file TTLs:**
    
    
    # PHP: set session.gc_maxlifetime and session.gc_probability in php.ini
    # Example: expire sessions after 1440 seconds (24 minutes), run GC on 1% of requests
    session.gc_maxlifetime = 1440
    session.gc_probability = 1
    session.gc_divisor = 100

**Use**`systemd-tmpfiles` for automatic cleanup of temp directories:
    
    
    # /etc/tmpfiles.d/app-sessions.conf
    # Delete files in /var/lib/php/sessions older than 1 day
    D /var/lib/php/sessions 0700 www-data www-data 1d

**Long-term: reformat the partition with more inodes** (requires downtime):
    
    
    # Only feasible if the filesystem can be unmounted and reformatted
    # Create ext4 with one inode per 4 KB instead of the default 16 KB:
    mkfs.ext4 -i 4096 /dev/sdb1
    # Warning: this reduces maximum file storage capacity; only appropriate
    # for partitions dedicated to small-file workloads

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`df -i`, `find ... \| wc -l`, `du --inodes`| None| Read-only investigation  
Deleting old session files| **Active sessions are terminated**|  Users with sessions in the deleted files are logged out; acceptable if files are older than the TTL, but confirm the TTL before deleting  
Deleting temp files| Minimal| Only affects processes that opened the file and expected it to persist — most temp files are safe; use `lsof` on suspect files if unsure  
Deleting build artifacts| None for running services| Only affects future builds; running services do not read `node_modules` or `.class` files at runtime after startup  
`postsuper -d ALL deferred`| **Undelivered mail is lost**|  Irreversible; only do this with explicit authorization from the mail system owner  
Reformatting the partition| **Full outage for the service using that partition**|  Requires unmounting; schedule a maintenance window  
  
**The primary operational risk is deleting active session files.** If session files are younger than the application's session TTL, logged-in users will be unexpectedly logged out. When in doubt, delete only files older than 2x the TTL, not all files.

* * *

## Verification
    
    
    # Confirm inodes are freed
    df -i
    # IUse% on the affected partition should be below 100%
    
    # Confirm file creation now works
    touch /tmp/inode-test-$$ && rm /tmp/inode-test-$$
    # Should complete without error
    
    # Confirm the triggering application has recovered
    # (restart if it cached the ENOSPC error)
    systemctl restart <affected-service>   # if needed

In Datadog, verify:

  * `system.fs.inodes.in_use` on the affected device drops below 0.80

  * Application error rate returns to baseline (no more `ENOSPC` errors in logs)

  * If a monitor on `system.fs.inodes.in_use` does not yet exist, create one now at 80% — this scenario gives no warning from `system.disk.in_use` alone




* * *

## Related Scenarios

  * If the inode-consuming workload is session files from a PHP or web application, the session accumulation rate is traffic-driven — a sudden traffic spike can also trigger this; monitor session TTL and GC settings proactively.

  * If the culprit is build artifacts under a build directory, co-locate with the Temp Files & Build Artifacts scenario for a broader cleanup of build-related disk consumers.

  * If `/var/lib/docker/overlay2/` is consuming inodes, it is the same Docker storage that causes byte-level disk bloat — refer to Disk: Docker Image and Build Cache Bloat for remediation steps.

  * After resolving the immediate crisis, add `system.fs.inodes.in_use` monitoring. This scenario is Critical precisely because it is invisible until the last inode is consumed.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6864963051 -->
