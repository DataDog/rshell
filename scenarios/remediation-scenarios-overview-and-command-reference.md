# Remediation Scenarios: Overview and Command Reference

Each scenario in this folder describes a specific host-level issue: its signal shape, how to investigate it at OS level, how to remediate it, and what service disruption to expect. Scenarios are grouped into three families based on the resource they affect and the IssueType value that the platform attaches to the alert.

Reference: [Q3 prioritized host issues](<https://docs.google.com/spreadsheets/d/17c1M5seb7fabfoFiHIaz63c40GxbMJ5MVGzu0fioBzY/edit?gid=2027518002#gid=2027518002>)

Issue Family / Type| Primary signal| Example scenarios  
---|---|---  
[**Disk**](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/folder/6845497987>)| `system.disk.in_use` high| Core Dumps, Core Dump Flood from Crash Loop, Docker Container Layers, Docker Image/Build Cache Bloat, Inode Exhaustion, Orphaned Postgres Replication Slot, Temp Files & Build Artifacts, Database Growth, Unrotated Logs  
[**Memory**](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/folder/6846317191>)| `system.mem.used` rising| Slow Application Leak, Unbounded Cache / Session Growth, Long-Running DB Transaction  
[**CPU**](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/folder/6846710160>)| `system.cpu.user` high| Cron Storm, Runaway Process / Infinite Loop, GC Pressure  
  
The signal shape is often the fastest way to route to the right scenario before reading further:

  * **Sudden spike, then stable** -> Core Dumps (single crash, disk)

  * **Rapid staircase rise + simultaneous service monitor ALERT or NO_DATA** -> Core Dump Flood from Crash Loop (disk)

  * **ENOSPC errors but**`df -h` shows free space -> Inode Exhaustion (disk)

  * **Periodic spike** -> Cron Storm (CPU)

  * **Steady rise correlated with traffic / business hours** -> Unrotated Logs (disk)

  * **Slow, steady rise** -> Database Growth (disk), Unbounded Cache (memory)

  * **Slow, steady rise on database volume;**`pg_wal/` large but table sizes stable -> Orphaned Postgres Replication Slot (disk)

  * **Rising sawtooth** -> Slow Application Leak (memory), GC Pressure (CPU)

  * **Slow, steady rise on Docker partition, proportional to deploy/build frequency** -> Docker Image/Build Cache Bloat (disk)

  * **Episodic or sudden rise on Docker partition after container lifecycle events** -> Docker Container Layers (disk)

  * **Continuous high CPU, single PID** -> Runaway Process / Infinite Loop (CPU)

  * **Disk rising on database volume + long**`pg_stat_activity` transaction age -> Long-Running DB Transaction (memory)




* * *

## Disk issues -- Command Reference

Covers: Core Dumps * Core Dump Flood from Crash Loop * Docker Container Layers * Docker Image/Build Cache Bloat * Inode Exhaustion * Orphaned Postgres Replication Slot * Temp Files & Build Artifacts * Database Growth * Unrotated Logs

 _**Privilege key:** _

  * ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) _Root / sudo = always requires elevated privilege_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _Context-dependent = depends on target files/processes or docker group membership_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _None = safe to run as a service account_




Tool| Scenarios| Stage| Privilege Required  
---|---|---|---  
`cat`| Core Dumps * Core Dump Flood * Logs| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`coredumpctl`| Core Dumps * Core Dump Flood| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (view: none; delete/manage: root)  
`df`| Core Dump Flood * Docker Image/Build Cache * Inode Exhaustion * Logs * Replication Slot| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`dmesg`| Core Dumps| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (CAP_SYS_ADMIN on Linux 5.x+)  
`docker buildx`| Docker Container Layers * Docker Image/Build Cache| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (docker group required; equivalent to root)  
`docker images`| Docker Container Layers * Docker Image/Build Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (docker group required; equivalent to root)  
`docker ps`| Docker Container Layers * Docker Image/Build Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (docker group required; equivalent to root)  
`docker system`| Docker Container Layers * Docker Image/Build Cache| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (docker group required; equivalent to root)  
`docker volume`| Docker Container Layers| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (docker group required; equivalent to root)  
`du`| Core Dumps * Core Dump Flood * Docker Container Layers * Docker Image/Build Cache * Inode Exhaustion * Replication Slot * Temp Files * Database * Logs| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (root-owned directories require sudo)  
`find`| Core Dumps * Core Dump Flood * Inode Exhaustion * Temp Files * Logs| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (root-owned directories require sudo)  
`go`| Temp Files| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`gradle`| Temp Files| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`grep`| Database| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`journalctl`| Core Dumps * Core Dump Flood * Database * Logs| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (system journal requires root; user journal does not)  
`logrotate`| Logs| both| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (system log files are root-owned)  
`ls`| Core Dumps * Core Dump Flood * Inode Exhaustion * Replication Slot * Logs| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`lsof`| Temp Files * Logs| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (full cross-process visibility requires root)  
`mysql`| Database| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (DB admin credentials required for some operations)  
`npm`| Temp Files| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`pip`| Temp Files| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`psql`| Database * Replication Slot| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)Context-dependent (DB superuser required for DROP REPLICATION SLOT and pg_terminate_backend)  
`rm`| Core Dumps * Core Dump Flood * Inode Exhaustion * Temp Files * Logs| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for system-owned files)  
`stat`| Inode Exhaustion| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`systemctl`| Core Dumps * Core Dump Flood * Database * Docker Image/Build Cache * Logs| both| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (service start/stop/restart requires root)  
`systemd-tmpfiles`| Inode Exhaustion * Temp Files| remediation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png)  
`sysctl`| Core Dump Flood| remediation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png)(kernel parameter writes require root)  
`truncate`| Logs| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for system-owned log files)  
`tune2fs`| Inode Exhaustion| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png)(filesystem-level changes require root)  
  
* * *

## Memory issues -- Command Reference

Covers: Slow Application Leak * Unbounded Cache / Session Growth * Long-Running DB Transaction

 _**Privilege key:** _

  * __![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) _Root / sudo = always requires elevated privilege_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _Context-dependent = depends on target files/processes or docker group membership_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _None = safe to run as a service account_




Tool| Scenarios| Stage| Privilege Required  
---|---|---|---  
`/proc/<PID>/smaps_rollup`| Slow Leak * Unbounded Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`/proc/<PID>/status`| Slow Leak * Unbounded Cache * DB Transaction| investigation| Context-dependent (root required for processes owned by other users)  
`cat`| DB Transaction| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`df`| DB Transaction| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`dmesg`| Slow Leak * Unbounded Cache| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (CAP_SYS_ADMIN on Linux 5.x+)  
`du`| DB Transaction| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root-owned directories require sudo)  
`free`| Slow Leak * Unbounded Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`grep`| DB Transaction| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`ls`| DB Transaction| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`ls /proc/<PID>/fd`| Slow Leak * Unbounded Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`pmap`| Slow Leak| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`ps`| Slow Leak * Unbounded Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`psql`| DB Transaction| both| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (DB superuser required for pg_terminate_backend)  
`ss`| Unbounded Cache| investigation| None  
`systemctl`| Slow Leak * Unbounded Cache| remediation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (service restart requires root)  
`top`| Slow Leak| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`vmstat`| Slow Leak * Unbounded Cache| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
  
* * *

## CPU issues -- Command Reference

Covers: Cron Storm * Runaway Process / Infinite Loop * GC Pressure

 _**Privilege key:** _

  * __![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) _Root / sudo = always requires elevated privilege_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _Context-dependent = depends on target files/processes or docker group membership_

  * ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) _None = safe to run as a service account_




Tool| Scenarios| Stage| Privilege Required  
---|---|---|---  
`/proc/<PID>/stat`| Runaway Loop * GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`/proc/<PID>/status`| GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`/proc/<PID>/wchan`| Runaway Loop| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required for processes owned by other users)  
`crontab`| Cron Storm| investigation| None (own crontab); root required to inspect other users' crontabs  
`dmesg`| GC Pressure| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png)(CAP_SYS_ADMIN on Linux 5.x+)  
`find`| GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root-owned directories require sudo)  
`flock`| Cron Storm| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`grep`| Cron Storm * GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`journalctl`| Cron Storm| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (system journal requires root; user journal does not)  
`kill`| Cron Storm * Runaway Loop| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (root required to signal another user's process)  
`nice`| Cron Storm| remediation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png) Context-dependent (negative nice values / priority increase require root)  
`perf`| Runaway Loop| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (requires CAP_SYS_ADMIN or CAP_PERFMON; often blocked in containers)  
`pgrep`| Cron Storm| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`ps`| Cron Storm * Runaway Loop * GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`strace`| Runaway Loop| investigation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (requires CAP_SYS_PTRACE to attach to another process)  
`systemctl`| Runaway Loop * GC Pressure| remediation| ![content.emoticon.crown](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/72/1f451.png) (service restart requires root)  
`top`| Cron Storm * Runaway Loop * GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`uptime`| Cron Storm * Runaway Loop| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)  
`vmstat`| Cron Storm * Runaway Loop * GC Pressure| investigation| ![\(blue star\)](https://datadoghq.atlassian.net/wiki/s/-436536684/6452/534630fb30addd8e6f6fdb1d30a20ca17b4fd5f6/_/images/icons/emoticons/star_blue.png)

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6846317195 -->
