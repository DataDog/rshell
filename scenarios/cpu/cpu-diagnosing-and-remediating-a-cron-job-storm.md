# CPU - Cron Storm

**Signal:** `system.cpu.user` or `system.load.1` spikes at predictable, recurring intervals aligned with a cron schedule  
**IssueType:** `cpu_usage`  
**Metric (typical):** `system.cpu.user`, `system.load.1`, `system.load.5`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`top`| investigation| `top -b -n 1 \| head -30`  
`ps`| investigation| `ps aux --sort=-%cpu \| head -20` · `ps -eo pid,ppid,cmd,etime \| grep <job>`  
`crontab`| investigation| `crontab -l` · `cat /etc/cron.d/*` · `cat /etc/crontab`  
`journalctl`| investigation| `journalctl -u cron --since "1 hour ago"` · `journalctl -u crond`  
`grep`| investigation| `grep CRON /var/log/syslog \| tail -50`  
`pgrep`| investigation| `pgrep -a -f <job-name>`  
`uptime`| investigation| `uptime`  
`vmstat`| investigation| `vmstat 1 15`  
`kill`| remediation| `kill <PID>` · `pkill -f <job-name>`  
`flock`| remediation| `flock -n /var/lock/<job>.lock <command>`  
`nice`| remediation| `nice -n 15 /path/to/job.sh`  
  
* * *

## What Happens

Multiple cron jobs fire simultaneously and compete for CPU. A single job can also trigger a storm if it fans out parallel child processes, or if previous instances have not finished before the next run starts, causing overlap accumulation over successive intervals.

Common causes:

  * All jobs scheduled at `0 * * * *` or `0 0 * * *` without schedule staggering

  * A job that takes longer than its run interval spawns a new instance while the previous one is still running; over time N overlapping instances pin the host

  * A job that itself parallelises heavily (`xargs -P`, `parallel`, or spawning many background subshells)

  * A configuration management tool (Puppet, Chef, Ansible) triggered by cron across many hosts simultaneously, causing a fleet-wide CPU spike

  * A maintenance cron (log rotation, database vacuuming, cache invalidation) that is heavier than expected and was not sized against the host's available CPU headroom




* * *

## Detection

Detected via `system.cpu.user` or `system.load.1` breaching the monitor threshold. The defining characteristic is periodicity: the spike recurs at fixed intervals aligned with a cron schedule (every hour, every 15 minutes, every midnight). This distinguishes it from a runaway loop (continuous, single-PID) or GC pressure (correlated with memory).

**Correlated signals to check:**

  * `system.load.1` spikes sharply and decays within minutes (duration approximates job runtime)

  * `system.cpu.iowait` elevated if the job is disk-heavy (log rotation, backup, database maintenance)

  * Application request error rate spiking at the same time: cron-induced CPU saturation can delay application thread scheduling

  * Multiple hosts spiking simultaneously: suggests a fleet-wide scheduled job (Puppet runs, centralized cron)




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Access to crontab files for the running user(s) and `/etc/cron.d/`| Needed to identify scheduled jobs and their timing  
Knowledge of the approximate time the spike occurs| Allows correlation of the spike time to cron schedule entries  
  
### Steps

  1. **Confirm the CPU spike is periodic**



    
    
    # In Datadog: view system.cpu.user over 24 h with 1 h granularity
    # Look for peaks at regular intervals (every hour, every 15 min, midnight, etc.)
    # Note the exact minute-of-hour when spikes occur

  2. **Identify which processes are consuming CPU during the spike**



    
    
    # During or just after a spike:
    ps aux --sort=-%cpu | head -20
    # Note: process name, PID, CPU%, elapsed time (TIME column)
    
    top -b -n 1 | head -30

  3. **Correlate spike time to cron schedule entries**



    
    
    # System-wide cron jobs
    cat /etc/crontab
    cat /etc/cron.d/*
    ls -la /etc/cron.hourly/ /etc/cron.daily/ /etc/cron.weekly/
    
    # Per-user crontab
    crontab -l
    # Run as other users if needed: sudo crontab -l -u <username>

  4. **Check cron execution logs**



    
    
    # systemd-based cron (Debian/Ubuntu)
    journalctl -u cron --since "2 hours ago" | grep -E "CMD|session"
    
    # syslog-based
    grep CRON /var/log/syslog | tail -100
    # Look for: (user) CMD (/path/to/script) entries matching the spike times

  5. **Check for overlapping job instances**



    
    
    pgrep -a -f <job-script-name>
    # Multiple PIDs with different start times = overlapping instances
    
    ps -eo pid,ppid,cmd,etime | grep <job-script-name>
    # ETIME shows elapsed time; overlapping instances have different values

  6. **Check load and CPU over a short window**



    
    
    vmstat 1 30
    # r column (run queue): if consistently > number of CPU cores, system is CPU-saturated
    uptime
    # Load average above CPU core count indicates saturation

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Access to edit crontab files| Required to reschedule or disable jobs  
Understand the job's purpose before killing it| Some jobs (backups, log rotation) must complete; killing mid-run may leave state inconsistent  
Coordinate schedule changes with team if the job is shared infrastructure| Rescheduling a job used by multiple teams requires alignment  
  
### Immediate Relief

**Kill excess overlapping instances (keep the oldest running instance):**
    
    
    # List all instances sorted by elapsed time
    ps -eo pid,etime,cmd | grep <job-name> | sort -k2
    # Kill all but the longest-running (oldest) instance
    kill <PID-of-newer-instances>

**If the job is safe to stop entirely:**
    
    
    pkill -f <job-script-name>

### Prevent Overlapping Runs

**Add**`flock` to enforce a single running instance:
    
    
    # -n exits immediately if the lock is already held (no queuing)
    * * * * * /usr/bin/flock -n /var/lock/myjob.lock /path/to/job.sh
    
    # Or wait up to 60 s before giving up
    * * * * * /usr/bin/flock -w 60 /var/lock/myjob.lock /path/to/job.sh

### Stagger Schedules

**Offset jobs that fire at round intervals:**
    
    
    # Before: all fire at the top of the hour
    0 * * * * /path/to/job-a.sh
    0 * * * * /path/to/job-b.sh
    0 * * * * /path/to/job-c.sh
    
    # After: spread across the hour
    2 * * * * /path/to/job-a.sh
    17 * * * * /path/to/job-b.sh
    34 * * * * /path/to/job-c.sh

**Use**`RandomizedDelaySec` (systemd timers) for fleet-wide staggering:
    
    
    # /etc/systemd/system/myjob.timer
    [Timer]
    OnCalendar=hourly
    RandomizedDelaySec=300   # up to 5-minute random offset per host

### Reduce Job Resource Usage

**Apply CPU scheduling limits to cap blast radius:**
    
    
    # Lower CPU scheduling priority
    * * * * * nice -n 15 /path/to/job.sh
    
    # Or throttle via cgroup using systemd-run
    * * * * * systemd-run --scope --slice=background.slice --property=CPUQuota=25% /path/to/job.sh

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Inspecting `ps`, crontab files, logs| None| Read-only  
`kill` on a single overlapping job instance| None for the application| The job instance is terminated; verify it is not mid-write to a critical file before killing  
`pkill -f <job-name>`| None for the application; job work is lost| Kills all matching instances; confirm the job is safe to abort  
Editing crontab to stagger schedules| None immediately| Only affects future runs  
Adding `flock` to cron entry| None immediately| Only affects future runs; prevents overlap going forward  
Adding `nice` or `CPUQuota`| None| Throttles future runs; does not stop current jobs  
Converting to systemd timer with `RandomizedDelaySec`| None| Requires creating `.service` and `.timer` units; the delay applies from the next scheduled run  
  
**Killing a mid-run backup or maintenance job is the highest-risk action.** Confirm the job is idempotent or can be safely re-run before terminating it.

* * *

## Verification
    
    
    # Confirm CPU has recovered
    top -b -n 1 | head -5
    uptime   # load average should be below CPU core count
    
    # Confirm no overlapping instances remain
    pgrep -a -f <job-script-name>
    
    # Confirm flock is effective: attempt to acquire the lock while the job is running
    flock -n /var/lock/myjob.lock echo "got lock" || echo "lock held — only one instance running"

In Datadog, verify:

  * `system.cpu.user` no longer spikes at the previously problematic schedule times

  * `system.load.1` stays below CPU core count throughout the schedule window

  * If staggered: the spike is smaller and offset from the original schedule time




* * *

## Related Scenarios

  * If the CPU spike is not periodic but still caused by many short-lived processes, check for a process supervisor restarting a crashing service in a tight loop (`systemctl status <service>` showing many restarts).

  * If the spike occurs on all hosts simultaneously, the job is triggered from a central scheduler (Rundeck, Airflow, CI pipeline) rather than local cron; the staggering fix must be applied at the scheduler level.

  * If `system.cpu.iowait` is elevated alongside CPU, the job is disk-bound; address disk throughput (see disk space scenarios) in addition to CPU scheduling.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6845989425 -->
