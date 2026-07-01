# Disk Space - Temp Files and Build Artifacts

**Signal:** `system.disk.in_use` high on root partition or a partition hosting build workdirs  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition; occasionally a dedicated `/build` or `/workspace` volume

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`du`| investigation| `du -h --max-depth=2 / \| sort -rh \| head -30` · `du -sh ~/.npm ~/.cache/pip ~/.m2 ~/.gradle/caches`  
`find`| both| `find /tmp /var/tmp -size +100M` · `find /tmp -mindepth 1 -mtime +1 -exec rm -rf {} +`  
`lsof`| investigation| `lsof /tmp`  
`npm`| both| `npm cache verify` · `npm cache clean --force`  
`pip`| both| `pip cache info` · `pip cache purge`  
`gradle`| remediation| `gradle --stop`  
`go`| remediation| `go clean -modcache`  
`rm`| remediation| `rm -rf ~/.m2/repository` · `rm -rf ~/.gradle/caches`  
`systemd-tmpfiles`| remediation| `systemd-tmpfiles --clean`  
  
* * *

## What Happens

Build systems, package managers, and runtime tools accumulate files in temporary and cache directories that are not automatically cleaned up. Unlike log accumulation (which is steady) or database growth (which is slow), this pattern often shows step-function spikes during CI job runs followed by a plateau when the job ends but artifacts are not cleaned.

Common sources:

  * **CI/CD pipelines** that clone repos, compile code, and produce artifacts (binaries, containers, test outputs) without a cleanup step

  * **Package manager caches** that download dependencies to local cache directories and never invalidate them: `~/.npm`, `~/.cache/pip`, `~/.m2/repository`, `~/.gradle/caches`

  * **/tmp not cleaned** : Long-running hosts accumulate temporary files from processes that crash or do not clean up after themselves; systemd-tmpfiles defaults clean `/tmp` on reboot, not on a schedule

  * **/var/tmp** : Unlike `/tmp`, this directory survives reboots and is rarely cleaned automatically

  * **Application working directories** that write intermediate files (extracted archives, decompressed data, temporary uploads) without a cleanup phase




* * *

## Detection

The platform detects this via `system.disk.in_use` breaching the threshold. The shape of the metric is the key diagnostic clue:

  * Spikes that correlate with CI job timing (check if the spike aligns with your pipeline schedule) suggest build artifact accumulation

  * Slow, continuous growth suggests package cache accumulation

  * Sudden spikes at random times may indicate an application writing large temp files without cleanup




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run on the host  
Read access to home directories and build workspaces| Package caches live in user home directories; access is needed to inspect them  
Knowledge of which CI system and build tool the host runs| Commands differ between npm, pip, Maven, Gradle, Bazel, etc.  
  
### Steps

  1. **Identify the largest consumers**



    
    
    du -h --max-depth=2 / 2>/dev/null | sort -rh | head -30
    # Focus on: /tmp, /var/tmp, /home, /root, /build, /workspace, /runner
    

  2. **Check /tmp and /var/tmp**



    
    
    du -sh /tmp /var/tmp
    find /tmp /var/tmp -size +100M -exec ls -lh {} \; 2>/dev/null
    find /tmp /var/tmp -mtime +1 -exec ls -lh {} \; 2>/dev/null | head -30
    # Files older than 1 day that are still in /tmp are often safe to remove
    

  3. **Check package manager caches**



    
    
    # npm (Node.js)
    du -sh ~/.npm 2>/dev/null
    npm cache verify 2>/dev/null
    
    # pip (Python)
    du -sh ~/.cache/pip 2>/dev/null
    pip cache info 2>/dev/null
    
    # Maven (Java)
    du -sh ~/.m2/repository 2>/dev/null
    
    # Gradle (Java/Kotlin)
    du -sh ~/.gradle/caches 2>/dev/null
    
    # Cargo (Rust)
    du -sh ~/.cargo/registry 2>/dev/null
    
    # Go module cache
    du -sh $(go env GOPATH)/pkg/mod 2>/dev/null
    

  4. **Check CI/CD workspace directories**



    
    
    # GitLab Runner
    du -sh /home/gitlab-runner/builds/ 2>/dev/null
    
    # GitHub Actions
    du -sh /home/runner/_work/ 2>/dev/null
    du -sh /opt/hostedtoolcache/ 2>/dev/null
    
    # Jenkins
    du -sh /var/lib/jenkins/workspace/ 2>/dev/null
    

  5. **Check for large application temp files**



    
    
    find /tmp /var/tmp /opt /srv -name "*.tmp" -o -name "*.part" -o -name "*.download" 2>/dev/null | xargs ls -lh 2>/dev/null | sort -k5 -rh | head -20
    

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Confirm no running process is actively using a file before deleting it| Deleting a file that a process has open does not reclaim space until the process closes or exits; the inode stays allocated  
Check with `lsof` before removing large files from /tmp| Some processes rely on specific temp file paths  
CI pipeline config access if adding a cleanup step| Requires repo access and a CI pipeline edit  
  
### Immediate Space Recovery

**Clean /tmp (check for active users first):**
    
    
    # Check if any process has files in /tmp open
    lsof /tmp 2>/dev/null | head -20
    
    # Delete files older than 1 day (safe on most hosts)
    find /tmp -mindepth 1 -mtime +1 -exec rm -rf {} + 2>/dev/null
    
    # Or delete everything that is not held open by a process
    find /tmp -mindepth 1 ! -exec fuser -s {} \; -exec rm -rf {} + 2>/dev/null
    

**Clean package manager caches:**
    
    
    npm cache clean --force
    
    pip cache purge
    
    # Maven: delete the entire local repository (will be re-downloaded on next build)
    rm -rf ~/.m2/repository
    
    # Gradle
    gradle --stop && rm -rf ~/.gradle/caches
    
    # Go
    go clean -modcache
    

**Clean CI workspace directories:**
    
    
    # GitLab Runner: remove old build directories
    find /home/gitlab-runner/builds/ -maxdepth 2 -mindepth 2 -mtime +7 -type d -exec rm -rf {} + 2>/dev/null
    
    # Jenkins: clean all workspaces from the Jenkins UI, or:
    find /var/lib/jenkins/workspace/ -maxdepth 1 -mindepth 1 -mtime +7 -exec rm -rf {} + 2>/dev/null
    

### Permanent Fix

**Add a cleanup step to CI pipelines:**
    
    
    # GitLab CI: always-run cleanup job
    cleanup:
      stage: .post
      script:
        - rm -rf build/ dist/ target/ node_modules/.cache/
        - docker system prune -f || true
      when: always
    
    # Or configure GitLab Runner to clean the workspace before each job
    # config.toml
    [[runners]]
      [runners.custom_build_dir]
        enabled = true
      [runners.cache]
        [runners.cache.s3]
      environment = ["GIT_CLEAN_FLAGS=-fdx"]
    

**Configure systemd-tmpfiles to clean /tmp on a schedule (not just on reboot):**
    
    
    # /etc/tmpfiles.d/tmp-cleanup.conf
    # Remove files in /tmp older than 3 days
    d /tmp 1777 root root 3d
    

Apply immediately:
    
    
    systemd-tmpfiles --clean
    

**Set up a cron job for package cache cleanup on build hosts:**
    
    
    # /etc/cron.d/build-cache-cleanup
    # Run at 2 AM daily
    0 2 * * * ci-user find /home/ci-user/.npm -mtime +14 -delete 2>/dev/null; pip cache purge 2>/dev/null
    

**For Maven and Gradle on CI:** Configure them to use a shared, version-tagged cache directory that is explicitly invalidated when dependencies change (avoid global `~/.m2` on shared build hosts).

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Delete files from /tmp older than N days| Low risk; potential process failure| Any process relying on a specific temp file path that gets deleted will fail. Use `lsof` to confirm no open file handles before mass deletion. Random temp file names (e.g., `/tmp/tmpXXXXXX`) are always safe to delete if the process that created them is no longer running  
`npm cache clean --force`| None for running services| Affects only future npm install runs, which will re-download dependencies (slower first run)  
`pip cache purge`| None for running services| Same as npm; existing installed packages are not affected  
`rm -rf ~/.m2/repository`| None for running services; next build is slower| Maven re-downloads all dependencies on the next build; this can take minutes on a cold cache  
`gradle --stop`| **Stops the Gradle build daemon**|  Any in-progress Gradle builds on that host are killed. Do not run during an active CI job  
`go clean -modcache`| None for running services| Affects only future builds  
Deleting CI workspace directories| **Kills in-progress CI jobs if their workspace is deleted**|  Always check if a job is running on the host before deleting its workspace directory. Use the CI platform's interface to drain or cancel jobs first  
Adding a cleanup step to a CI pipeline| None| Only affects future pipeline runs  
Configuring systemd-tmpfiles| None at config time| `systemd-tmpfiles --clean` runs a one-shot cleanup pass; does not affect running services unless they happen to own a file being cleaned (which would only occur if the file is older than the configured age)  
Deleting entire npm `node_modules` (if present)| **Application restart required**|  If a running Node.js application loaded modules from the deleted `node_modules`, it will crash or behave incorrectly. Only delete `node_modules` when the application is stopped or if it is a build-time directory not used at runtime  
  
**Key consideration for CI hosts:** A CI runner may be executing a job at the time cleanup is triggered. Always drain or pause the runner before performing aggressive cleanup (workspace deletion, Gradle daemon stop). For cloud-based ephemeral runners, this is not an issue since the host terminates after the job.

* * *

## Verification
    
    
    # Confirm space has been reclaimed
    df -h <device>
    
    # Confirm /tmp is clean
    du -sh /tmp
    
    # Confirm caches have been cleared
    du -sh ~/.npm ~/.cache/pip ~/.m2 ~/.gradle/caches 2>/dev/null
    
    # In Datadog
    # avg:system.disk.in_use{host:<hostname>,device:<device>}
    # Should drop below warning threshold within 1-5 minutes
    

* * *

## Related Scenarios

  * If cleanup recovers space but the disk fills again within hours during CI runs, the pipeline cleanup step is missing or ineffective; prioritize the permanent fix.

  * If `/tmp` fills rapidly without a CI job running, an application is writing large temp files without cleanup; use `lsof` and `inotifywait` to identify the writing process in real time.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6546328555 -->
