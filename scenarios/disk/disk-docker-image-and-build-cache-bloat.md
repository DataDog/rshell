# Disk - Docker Image and Build Cache Bloat

**Signal:** `system.disk.in_use` rising steadily on the partition hosting `/var/lib/docker`; `docker system df` shows large RECLAIMABLE on Images or Build Cache  
**IssueType:** `disk_usage`  
**Device (typical):** Root partition or a dedicated Docker data volume (`/dev/sda1`, `/dev/nvme0n1p1`)

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`docker system`| both| `docker system df` · `docker system df -v` · `docker system prune -f` · `docker system prune -a -f`  
`docker images`| investigation| `docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}"` · `docker images -f dangling=true`  
`docker buildx`| both| `docker buildx du` · `docker buildx prune -f`  
`docker ps`| investigation| `docker ps -a --filter status=exited`  
`du`| investigation| `du -sh /var/lib/docker/` · `du -sh /var/lib/docker/buildkit/`  
`df`| investigation| `df -h /var/lib/docker`  
  
* * *

## What Happens

On any host that builds or pulls Docker images (CI runners, build agents, deploy nodes), Docker's storage directory grows continuously unless cleanup runs regularly:

  * **Image accumulation** : every service deploy pushes a new image tag; the old tag is untagged (becoming "dangling") but the layers remain on disk. A host that deploys a service multiple times per day accumulates dozens of image versions within weeks.

  * **Build cache growth** : BuildKit retains every intermediate layer from every `docker build` invocation. This cache is invisible to `docker images` — it only appears in `docker system df` and `docker buildx du`. On active build hosts it routinely reaches 10-30 GB with no running containers involved.

  * **Stopped container filesystems** : containers that exited (from CI jobs, one-off tasks, or crashes) retain their writable layer on disk until explicitly removed.




The failure mode is distinctive: disk fills enough to block new image pulls or builds, but **running containers continue operating normally** until they need to write to disk themselves. This means the first observable symptom is deployment or CI pipeline failures, not a service crash.

Common host types affected:

  * CI runners that build Docker images

  * Kubernetes nodes with aggressive pull-always policies across many services

  * Hosts where services are deployed by updating a container (pull new image, stop old container, start new)

  * Developer workstations used for image building over months




* * *

## Detection

Detected via `system.disk.in_use` monitor. The signal shape is a **slow, steady rise over days or weeks** that accelerates on hosts with frequent builds or deploys. Unlike core dumps (sudden spike) or log rotation failures (traffic-correlated rise), this grows at roughly a constant rate tied to deployment frequency.

**First failure mode before disk full:**

The partition hosting `/var/lib/docker` typically hits trouble around 85-90% — `docker pull` and `docker build` begin failing with `no space left on device` while the rest of the host (running containers, application logs) is still healthy. Alert on 80% to leave room for the cleanup operation itself.

**Correlated signals:**

  * CI pipeline failures or deploy jobs failing with `no space left on device`

  * `docker pull` errors in service logs around the time of a deployment

  * `system.disk.in_use` rising on the Docker volume without a corresponding rise on other volumes




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run locally  
Member of `docker` group or root| All `docker` commands require daemon access  
Docker daemon is running| `docker system df` and `docker buildx du` require the daemon  
  
### Steps

  1. **Confirm Docker is the consumer**



    
    
    df -h /var/lib/docker   # or: df -h / if Docker is on root
    du -sh /var/lib/docker/

  2. **Get the full breakdown**



    
    
    docker system df
    # Output shows RECLAIMABLE per category:
    # TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE
    # Images          47        12        18.4GB    14.2GB (77%)
    # Containers      23        3         1.1GB     1.0GB (94%)
    # Local Volumes   11        4         3.2GB     1.8GB (56%)
    # Build Cache     -         -         4.7GB     4.7GB

Focus on Images and Build Cache — these are the two categories most likely to be large on a host that builds or deploys containers. If Build Cache alone is many GB, this is a build-agent host with no cache eviction configured.

  3. **Identify which images are accumulating**



    
    
    # All images sorted by size (largest first)
    docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}" \
      | sort -k3 -h -r | head -20
    
    # Dangling images only (untagged; safe to remove)
    docker images -f dangling=true
    
    # Count of images per repository to spot accumulation
    docker images --format "{{.Repository}}" | sort | uniq -c | sort -rn | head -10

  4. **Check build cache breakdown**



    
    
    docker buildx du 2>/dev/null || docker builder du
    # Shows cache entries with size and last-used time

  5. **Check stopped containers**



    
    
    docker ps -a --filter status=exited --format "table {{.Names}}\t{{.Status}}\t{{.Size}}\t{{.Image}}"
    # Large size values here mean the container wrote significant data before exiting

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
No deployment in-flight| `docker system prune -a` during an active image pull can remove intermediate layers mid-transfer, causing the pull to fail and the deployment to retry. Check with the deploy system before running  
Confirm named volumes are not needed| `docker system prune` without `--volumes` is safe; only add `--volumes` after confirming orphaned volumes hold no state you need  
  
### Immediate Space Recovery

**Step 1 — safe prune (stopped containers, dangling images, unused networks, build cache; does NOT remove tagged images in use by any container):**
    
    
    docker system prune -f

**Step 2 — if more space is needed: remove tagged images not referenced by any running or stopped container:**
    
    
    docker system prune -a -f

This is safe if the host can re-pull images on next deploy. Avoid during an active rolling deployment.

**Build cache only (zero runtime risk):**
    
    
    docker buildx prune -f
    # or
    docker builder prune -f

**Verbose output to see what was actually freed:**
    
    
    docker system prune -a -f 2>&1 | tail -5
    # "Total reclaimed space: 14.3GB"

### Prevent Recurrence

**Option 1 — scheduled prune cron job (recommended for CI/build hosts):**
    
    
    # /etc/cron.d/docker-cleanup
    # Prune nightly; keep images used in the last 48 hours
    0 3 * * * root docker system prune -a -f --filter "until=48h" >> /var/log/docker-cleanup.log 2>&1

**Option 2 — limit BuildKit cache size in the daemon (Docker 23+):**
    
    
    // /etc/docker/daemon.json  — add or merge:
    {
      "builder": {
        "gc": {
          "enabled": true,
          "defaultKeepStorage": "20GB"
        }
      }
    }

Then reload: `systemctl restart docker` (note: this restarts all containers; see Service Impact).

**Option 3 — add a cleanup step to every CI pipeline:**
    
    
    # GitLab CI example
    cleanup:
      stage: .post
      script:
        - docker system prune -f
      when: always

For comprehensive guidance on daemon-level configuration and edge cases (overlay2 storage driver, devicemapper, Docker-in-Kubernetes), see [Disk Space Management for Docker: Investigating and Remediating Container Layer Usage](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6548229392/Disk+Space+Management+for+Docker+Investigating+and+Remediating+Container+Layer+Usage>).

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`docker system df`| None| Read-only query to the daemon  
`docker system prune -f`| None| Only removes stopped containers and dangling images; running containers and their images are untouched  
`docker system prune -a -f`| None for running containers| Removes unused tagged images too; safe unless a deployment is actively pulling an image mid-transfer  
`docker buildx prune -f`| None| Build cache only; zero runtime impact  
`systemctl restart docker`| **Full host container outage**|  Stops ALL running containers; required only if changing `daemon.json`. Plan a maintenance window and coordinate with container owners.  
  
**The deployment-failure-before-crash pattern is the key distinguishing feature of this scenario.** When `/var/lib/docker` fills up, `docker pull` and `docker build` fail immediately. Running containers continue until they need to write to disk (logs, tmp files). The host is not "down" but all new deployments and CI jobs to it will fail.

* * *

## Verification
    
    
    # Confirm space reclaimed
    docker system df            # RECLAIMABLE should be near 0 on Images and Build Cache
    df -h /var/lib/docker       # usage should be below threshold
    
    # Confirm next deploy can pull
    docker pull <registry>/<image>:<tag>   # should succeed without error

In Datadog, verify:

  * `system.disk.in_use` on the Docker volume drops below the alert threshold within a few minutes of the prune completing

  * Subsequent CI pipeline runs or deploy jobs succeed without `no space left on device` errors




* * *

## Related Scenarios

  * If stopped containers are large contributors (large size in `docker ps -a`), investigate whether services are writing to container-local paths instead of mounted volumes — data written inside a container accumulates in its writable layer; see the Temp Files & Build Artifacts scenario for general writable-layer cleanup patterns.

  * If the disk is already full and `docker system prune` itself fails with `no space left on device`, you need to free space outside of Docker first (delete log files, clear `/tmp`) before the Docker daemon can complete its cleanup.

  * For daemon-level configuration, overlay2 edge cases, and devicemapper storage driver details, see [Disk Space Management for Docker: Investigating and Remediating Container Layer Usage](<https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6548229392/Disk+Space+Management+for+Docker+Investigating+and+Remediating+Container+Layer+Usage>).

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6863784788 -->
