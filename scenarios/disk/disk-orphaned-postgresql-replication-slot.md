# Disk - Orphaned PostgreSQL Replication Slot

**Signal:** `system.disk.in_use` rising continuously on the PostgreSQL data volume; `pg_replication_slots` shows one or more slots with `active = false`; WAL directory growing without bound  
**IssueType:** `disk_usage`  
**Device (typical):** Dedicated PostgreSQL data volume or root partition hosting `$PGDATA`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`psql`| both| `SELECT ... FROM pg_replication_slots` · `SELECT pg_drop_replication_slot('name')` · `SELECT ... FROM pg_stat_replication`  
`du`| investigation| `du -sh $PGDATA/pg_wal/`  
`ls`| investigation| `ls $PGDATA/pg_wal/ \| wc -l`  
`df`| investigation| `df -h $PGDATA`  
  
* * *

## What Happens

A PostgreSQL **replication slot** is a guarantee: the primary will retain all WAL segments produced since the slot's `restart_lsn` until the slot's consumer confirms it has processed them. Slots are used by physical streaming replicas, logical replication subscribers, and CDC tools (Debezium, pglogical, etc.).

When the consumer of a slot goes away without calling `pg_drop_replication_slot()` — a replica decommissioned without cleanup, a CDC pipeline that failed and was abandoned, a logical subscriber that was deleted at the application layer but not in the database — the slot remains. It is now **orphaned** : `active = false`, no connection associated, and `restart_lsn` frozen at the point when the consumer last confirmed.

PostgreSQL keeps every WAL segment since that frozen `restart_lsn` on disk, indefinitely. Unlike a long-running transaction (which will eventually commit or roll back), an orphaned slot has no natural expiry. On a busy primary, this can mean gigabytes of WAL accumulate per hour with no ceiling. The primary will eventually halt all writes when the data volume fills.

**Why it is hard to catch early** : the `system.disk.in_use` rise looks identical to normal database growth. The only distinguishing signal is correlating WAL directory size with `pg_replication_slots` — which requires database-level visibility that standard host monitoring does not provide by default.

**Common causes** :

  * A replica host was decommissioned (terminated, rebuilt) without running `pg_drop_replication_slot()` on the primary first

  * A Debezium, pglogical, or other CDC connector was undeployed or failed permanently; the database slot was never cleaned up

  * A logical replication subscriber table was dropped but the subscription was not, leaving the publisher-side slot inactive

  * A standby was promoted and the old primary's slot for it was never removed

  * A developer created a slot for testing (`pg_create_logical_replication_slot`) and forgot to drop it




* * *

## Detection

Detected via `system.disk.in_use` on the PostgreSQL data volume. The signal shape is a **slow, steady rise** — indistinguishable from normal database growth until correlated with WAL directory size and slot state.

**Correlated signals that narrow it to a slot problem** :

  * `du -sh $PGDATA/pg_wal/` is large and growing while database table sizes (`pg_database_size()`) are stable

  * `pg_replication_slots` has rows with `active = false`

  * `postgresql.replication.delay` shows zero or no connected replicas while a slot still exists




**Estimate time-to-disk-full from WAL growth rate** :
    
    
    # Run twice, 60 seconds apart; compute delta
    du -sh $PGDATA/pg_wal/
    # If WAL grows ~500 MB/min on a write-heavy primary: hours, not days, until full

* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Required to check WAL directory size  
`psql` access with at least `pg_monitor` role| Sufficient to query `pg_replication_slots` and `pg_stat_replication`; `pg_drop_replication_slot` requires superuser  
Know the value of `$PGDATA`| Needed to find the WAL directory; retrieve with `SHOW data_directory;` if unknown  
  
### Steps

  1. **Check WAL directory size and headroom**



    
    
    sudo -u postgres psql -c "SHOW data_directory;"
    
    du -sh $PGDATA/pg_wal/
    ls $PGDATA/pg_wal/ | wc -l   # each segment is typically 16 MB
    df -h $PGDATA

  2. **List all replication slots and their WAL retention**



    
    
    SELECT slot_name,
           slot_type,
           active,
           active_pid,
           restart_lsn,
           confirmed_flush_lsn,
           pg_size_pretty(
             pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)
           ) AS wal_retained
    FROM pg_replication_slots
    ORDER BY active, restart_lsn;

Focus on rows where `active = false`. The `wal_retained` column shows exactly how much WAL is being held on disk for each inactive slot. A value of many GB from an inactive slot is the smoking gun.

  3. **Confirm no active replica is using the slot**



    
    
    -- Active streaming connections (connected replicas/subscribers)
    SELECT application_name, state, sent_lsn, write_lsn, flush_lsn, replay_lsn,
           sync_state
    FROM pg_stat_replication;
    
    -- Cross-reference: if a slot appears in pg_replication_slots with active=false
    -- and does NOT appear in pg_stat_replication, it has no active consumer

  4. **Check when the slot last advanced (proxy: WAL lag age)**



    
    
    -- How far behind is the slot's restart_lsn from the current WAL position?
    -- This tells you approximately how long the slot has been idle
    SELECT slot_name,
           active,
           pg_size_pretty(
             pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)
           ) AS wal_lag,
           pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) AS wal_lag_bytes
    FROM pg_replication_slots
    WHERE NOT active
    ORDER BY wal_lag_bytes DESC;

  5. **Check for**`max_slot_wal_keep_size` setting (PostgreSQL 13+)



    
    
    SHOW max_slot_wal_keep_size;
    -- '-1' means unlimited — no cap on WAL retained per slot
    -- A positive value (e.g. '10GB') means PostgreSQL will invalidate the slot
    -- rather than retain more WAL than the limit, protecting disk at the cost
    -- of forcing the slot's consumer to rebuild from scratch

* * *

## Remediation

**Before dropping a slot: confirm it is truly orphaned.** Dropping a slot for a replica that is temporarily disconnected (network partition, restart) causes that replica to fall irrecoverably behind — it will need a full base backup to resync. The cost of a false positive is high.

### Preconditions

Precondition| Rationale  
---|---  
Confirm with the DBA or infra team that the slot's consumer no longer exists| The slot name often identifies the consumer (`debezium`, `replica_us_east`, etc.); verify the named system is decommissioned, not just temporarily offline  
Superuser or `pg_drop_replication_slot` privilege| Required to drop a slot; `pg_monitor` alone is not sufficient  
Notify the owning team before dropping| If the slot was for a replica, dropping it makes that replica's WAL stream permanently invalid; the replica owner must know to rebuild  
  
### Drop the orphaned slot
    
    
    -- Verify one more time before dropping
    SELECT slot_name, active, wal_retained
    FROM (
      SELECT slot_name, active,
             pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS wal_retained
      FROM pg_replication_slots
    ) s
    WHERE slot_name = '<slot_name>';
    
    -- Drop (irreversible)
    SELECT pg_drop_replication_slot('<slot_name>');
    
    -- Confirm it is gone
    SELECT slot_name FROM pg_replication_slots;

### WAL reclaim after drop

WAL recycling is automatic once the slot is dropped and the next checkpoint completes. The WAL directory will shrink over the next few minutes as PostgreSQL recycles segments it no longer needs to retain.
    
    
    # Poll to confirm WAL is shrinking
    while true; do
      echo "$(date +%T)  WAL=$(du -sh $PGDATA/pg_wal/ | cut -f1)"
      sleep 30
    done

### Prevent recurrence

**Set**`max_slot_wal_keep_size` (PostgreSQL 13+) — caps WAL retained per slot; the slot is invalidated rather than filling the disk:
    
    
    -- Apply globally via postgresql.conf
    ALTER SYSTEM SET max_slot_wal_keep_size = '10GB';
    SELECT pg_reload_conf();
    
    -- Verify
    SHOW max_slot_wal_keep_size;

When a slot is invalidated by this limit, PostgreSQL sets `pg_replication_slots.conflicting = true`; the consumer must perform a full resync. This is a loud failure (the consumer breaks) rather than a silent disk-fill. Prefer loud failures.

**Add a slot monitoring query as a Datadog custom check or alerting rule** :
    
    
    -- Alert if any inactive slot is retaining more than 5 GB of WAL
    SELECT slot_name,
           pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS wal_retained
    FROM pg_replication_slots
    WHERE NOT active
      AND pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) > 5 * 1024^3;

**Operational hygiene** : add slot cleanup to any replica decommission checklist. The slot must be dropped on the primary before the replica host is terminated.

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Querying `pg_replication_slots`, `pg_stat_replication`| None| Read-only  
`pg_drop_replication_slot()`| None for the primary| The primary continues running normally; WAL recycling resumes automatically  
`pg_drop_replication_slot()`| **Replica/consumer is permanently invalidated**|  Any replica or subscriber that was using this slot — even if temporarily offline — can no longer catch up; it must be rebuilt from a base backup. This is irreversible.  
`ALTER SYSTEM SET max_slot_wal_keep_size + pg_reload_conf()`| None| Applies immediately to slot WAL retention limits; no restart required  
  
`pg_drop_replication_slot` is the single high-risk action in this runbook. The disk impact is zero-risk (WAL is post-consumer data); the replica impact is severe and irreversible if the consumer is not actually orphaned. Always confirm consumer state before dropping.

* * *

## Verification
    
    
    -- Confirm the slot is gone
    SELECT slot_name FROM pg_replication_slots;
    -- Should not include the dropped slot
    
    -- Confirm WAL is shrinking
    -- du -sh $PGDATA/pg_wal/   (run twice 60 s apart)
    
    -- Confirm no remaining inactive slots with large WAL retention
    SELECT slot_name, active,
           pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS wal_retained
    FROM pg_replication_slots
    WHERE NOT active;
    -- Should return 0 rows (or rows with negligible retained WAL)

In Datadog, verify:

  * `system.disk.in_use` on the PostgreSQL data volume levels off and begins to drop as WAL is recycled

  * No new ALERT from the disk monitor within 30 minutes




* * *

## Related Scenarios

  * If WAL growth was accompanied by table bloat (dead tuples not reclaimed by VACUUM), a long-running transaction may also have been blocking VACUUM during the period the slot was inactive; see Memory: Long-Running DB Transaction for investigation steps.

  * If the WAL directory filled the disk before the slot was dropped, PostgreSQL may have paused writes; free space on the volume (or drop the slot) before the database can resume accepting writes.

  * If `max_slot_wal_keep_size` invalidated the slot before you could investigate (PostgreSQL 13+, positive limit set), the slot's `conflicting` flag is true and `restart_lsn` is null; the consumer must rebuild from a base backup regardless.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6866763812 -->
