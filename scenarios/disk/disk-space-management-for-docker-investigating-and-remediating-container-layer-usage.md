# Disk Space - Container Layers

**Signal:** `system.disk.in_use` high on root partition or a partition mounting `/var/lib/docker`  
**IssueType:** `disk_usage`  
**Device (typical):** `/dev/sda1`, `/dev/nvme0n1p1`, or a dedicated Docker data volume

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`du`| investigation| `du -sh /var/lib/docker`  
`docker system`| both| `docker system df` · `docker system prune -f` · `docker system prune -a -f`  
`docker ps`| investigation| `docker ps -a`  
`docker images`| investigation| `docker images -f dangling=true`  
`docker volume`| both| `docker volume ls -f dangling=true` · `docker volume prune -f`  
`docker buildx`| both| `docker buildx du` · `docker buildx prune -f`  
  
* * *

## What Happens

Docker stores image layers, container filesystems, build cache, and volumes under `/var/lib/docker`. Without periodic cleanup, this directory grows continuously as:

  * New image versions are pulled but old tags are not removed

  * Containers exit or crash and are never removed (`docker ps -a` shows many stopped containers)

  * BuildKit cache accumulates from repeated `docker build` runs

  * Named or anonymous volumes are orphaned when their containers are deleted

  * Multi-stage build intermediate layers are retained by default




On CI hosts or hosts that frequently deploy new versions, this can consume tens of GBs over weeks.

* * *

## Detection

Detected via `system.disk.in_use` monitor (type `HOST_DISK_USAGE`). The device in `HostMetadata` will be whichever partition `/var/lib/docker` resides on. If Docker has its own dedicated volume, that device appears directly.

**Secondary signal:** If the host's health score also shows elevated error spans, the disk pressure may be causing container OOM kills or failed deployments, which compounds the issue.

* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Commands must run locally  
Permission to run `docker` commands (member of `docker` group or root)| Docker daemon queries require this  
Docker daemon is running| `docker system df` requires the daemon to be up  
No active container that uses volumes under investigation| Must confirm volumes are safe to remove  
  
### Steps

  1. **Confirm Docker is the consumer**



    
    
    du -sh /var/lib/docker
    # If this is a significant fraction of disk usage, Docker is the culprit
    

  2. **Get a full Docker disk usage breakdown**



    
    
    docker system df
    # Output:
    # TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE
    # Images          47        12        18.4GB    14.2GB (77%)
    # Containers      23        3         1.1GB     1.0GB (94%)
    # Local Volumes   11        4         3.2GB     1.8GB (56%)
    # Build Cache     -         -         4.7GB     4.7GB
    

  3. **Identify stale images**



    
    
    # Dangling images (untagged, no longer referenced by any container)
    docker images -f dangling=true
    
    # All images with their age
    docker images --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}"
    
    # Images not used by any running or stopped container
    docker image ls --filter "dangling=false" | head -30
    

  4. **Identify stopped containers**



    
    
    docker ps -a --format "table {{.Names}}\t{{.Status}}\t{{.Size}}\t{{.Image}}"
    # Containers with status "Exited" or "Created" are reclaimable
    

  5. **Identify orphaned volumes**



    
    
    docker volume ls -f dangling=true
    # These volumes have no container referencing them
    

  6. **Check BuildKit cache**



    
    
    docker buildx du 2>/dev/null || docker builder du
    # Shows cache size and age
    

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Docker daemon must be running| `docker system prune` requires the daemon  
No critical stopped containers should be removed| Stopped containers may hold data or be intentionally paused; confirm with the owning team  
Named volumes must be confirmed as disposable| Volumes may contain persistent state (databases, user uploads); check before pruning  
Active deployments should not be in-flight| Pruning during a deployment can remove layers needed by a pull in progress  
  
### Immediate Space Recovery

**Safe prune (removes stopped containers, dangling images, unused networks; does NOT remove volumes or images in use):**
    
    
    docker system prune -f
    

**Remove unused images including tagged images not referenced by any container:**
    
    
    # More aggressive: removes all images not used by a running container
    docker system prune -a -f
    

**Remove orphaned volumes (only after confirming they hold no needed data):**
    
    
    docker volume prune -f
    

**Remove BuildKit cache:**
    
    
    docker buildx prune -f
    # or
    docker builder prune -f
    

**All-in-one (most aggressive; run with caution on production hosts):**
    
    
    docker system prune -a --volumes -f
    

### Permanent Fix

**Add a scheduled cleanup cron job:**
    
    
    # /etc/cron.d/docker-cleanup
    # Run at 3 AM daily; keep images used in the last 48 hours
    0 3 * * * root docker system prune -f --filter "until=48h" >> /var/log/docker-cleanup.log 2>&1
    

**Or configure the Docker daemon to limit image retention (Docker 25+):**
    
    
    // /etc/docker/daemon.json
    {
      "image-gc-high-threshold": 85,
      "image-gc-low-threshold": 80
    }
    

Restart the daemon after changing `daemon.json`:
    
    
    systemctl restart docker
    

**For CI hosts specifically:** Add a cleanup step at the end of each CI job or pipeline:
    
    
    # Example: GitLab CI cleanup job
    cleanup:
      stage: cleanup
      script:
        - docker system prune -f
      when: always
    

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`docker system prune -f`| None| Only removes stopped containers and dangling images; running containers are untouched  
`docker system prune -a -f`| None for running containers| Removes unused tagged images too; safe unless a deployment is actively pulling an image (race condition: the layer being pulled may be pruned mid-transfer, causing the pull to fail and the deployment to retry)  
`docker volume prune -f`| None| Only removes volumes with no container attached; volumes in use by running containers are skipped  
`docker buildx prune -f`| None| Build cache only; zero runtime impact  
Adding a cleanup cron job| None| Only affects future scheduled runs  
Changing `daemon.json` \+ `systemctl restart docker`| **Full outage on the host**|  Restarting the Docker daemon stops ALL running containers. This is a last-resort action; coordinate with container owners and schedule a maintenance window  
Configuring `image-gc-high-threshold` in `daemon.json`| **Requires daemon restart** (see above)| Plan accordingly  
  
**Key risk - deployment race condition:** If a new image is being pulled for a rolling deployment at the same time as `docker system prune -a`, the prune may remove intermediate layers that the pull depends on. The pull will fail and retry, causing a brief deployment hiccup. Avoid running aggressive prune commands during active deployments.

**Docker daemon restart is the one action that causes a full host-level container outage.** It is almost never necessary for disk cleanup alone. Only do it if changing `daemon.json` settings that cannot be applied at runtime.

* * *

## Verification
    
    
    # On the host
    docker system df        # reclaimable should now be near zero
    df -h /var/lib/docker   # or df -h / if Docker is on root
    
    # In Datadog
    # avg:system.disk.in_use{host:<hostname>,device:<device>}
    # Should drop below warning threshold within 1-5 minutes
    

* * *

## Edge Cases

  * **Overlay2 storage driver:** Disk usage reported by `du -sh /var/lib/docker` may differ from what `docker system df` reports because overlay2 uses hard links. Trust `docker system df` as the authoritative source.

  * **devicemapper storage driver (older setups):** The thin pool may report usage differently from the filesystem. Check `dmsetup status` as well.

  * **Docker running in Kubernetes (DinD):** The host may be a Kubernetes node; coordinate volume pruning with the cluster operator to avoid removing node-level cache needed by kubelet.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6548229392 -->
