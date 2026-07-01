# Disk Space - Database Growth

**Signal:** `system.disk.in_use` high on the database data partition  
**IssueType:** `disk_usage`  
**Device (typical):** `/dev/sdb1`, `/dev/nvme1n1`, or a dedicated mount like `/var/lib/postgresql` or `/var/lib/mysql`

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`du`| investigation| `du -sh /var/lib/postgresql/` · `du -sh /var/lib/mysql/`  
`psql`| both| `psql -U postgres -c "SELECT relname, pg_size_pretty(...) FROM pg_stat_user_tables ..."` · `VACUUM ANALYZE <table>` · `REINDEX INDEX CONCURRENTLY <index>`  
`mysql`| both| `mysql -e "SHOW BINARY LOGS;"` · `PURGE BINARY LOGS BEFORE NOW() - INTERVAL 7 DAY;`  
`grep`| investigation| `grep -i "cleanup\|purge" /etc/cron.d/*`  
`systemctl`| investigation| `systemctl list-timers \| grep -i vacuum`  
`journalctl`| investigation| `journalctl --since "7 days ago" \| grep -i "vacuum\|retention"`  
  
* * *

## What Happens

Database storage grows when data is inserted faster than it is removed, or when the database engine retains internal overhead that is not automatically reclaimed. Unlike log files, database growth is typically slow and steady unless a specific process has gone wrong. Root causes include:

  * Missing or misconfigured data retention policies (no scheduled DELETE or PURGE jobs)

  * A retention cleanup job that stopped running silently (cron failure, job crash)

  * PostgreSQL table bloat: rows are logically deleted but the dead tuples are not yet vacuumed; the physical file stays large

  * PostgreSQL index bloat: indexes grow from updates/deletes but never shrink without a REINDEX

  * MySQL binary log accumulation: replication logs not purged after the retention period

  * Uncontrolled growth of a time-series or event table with no partitioning or TTL

  * A runaway batch import or backfill job that inserted far more data than expected




* * *

## Detection

The platform detects this via `system.disk.in_use` on the database volume. Growth is typically gradual; the metric trends upward over days or weeks before breaching the threshold. This distinguishes it from core dumps (sudden spike) or log storms (rapid rise during an incident).

**Correlated signals to check:**

  * APM traces showing slow queries on the affected database (table bloat increases sequential scan cost)

  * Application error spans mentioning `disk full`, `no space left on device`, or `could not write to file`

  * Database-specific metrics in Datadog if the database integration is configured:

    * `postgresql.table.bloat` (if the Postgres integration is running)

    * `mysql.replication.slave_running` going to 0 (replication can fail when bin logs fill the disk)




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Required to inspect the filesystem and run database clients  
Database client available on the host (`psql`, `mysql`, `redis-cli`)| Needed for introspection queries  
Read-only database credentials at minimum| Introspection queries require a database connection  
Knowledge of which database engine and version is running| Commands differ significantly between PostgreSQL, MySQL, and others  
  
### Steps

  1. **Confirm the database directory is the consumer**



    
    
    # PostgreSQL default
    du -sh /var/lib/postgresql/
    
    # MySQL/MariaDB default
    du -sh /var/lib/mysql/
    
    # Check per-database directory sizes
    du -sh /var/lib/postgresql/<version>/main/base/*/
    

  2. **PostgreSQL: find the largest tables and databases**



    
    
    -- Connect: psql -U postgres
    
    -- Total size per database
    SELECT datname, pg_size_pretty(pg_database_size(datname)) AS size
    FROM pg_database
    ORDER BY pg_database_size(datname) DESC;
    
    -- Largest tables in current database (including indexes and TOAST)
    SELECT relname,
           pg_size_pretty(pg_total_relation_size(relid)) AS total,
           pg_size_pretty(pg_relation_size(relid))        AS table_only,
           pg_size_pretty(pg_total_relation_size(relid)
                        - pg_relation_size(relid))         AS indexes_and_toast
    FROM pg_stat_user_tables
    ORDER BY pg_total_relation_size(relid) DESC
    LIMIT 20;
    

  3. **PostgreSQL: measure dead tuple bloat**



    
    
    -- Tables with the most dead tuples (candidates for VACUUM)
    SELECT relname,
           n_dead_tup,
           n_live_tup,
           round(n_dead_tup::numeric / NULLIF(n_live_tup + n_dead_tup, 0) * 100, 1) AS dead_pct,
           last_autovacuum,
           last_vacuum
    FROM pg_stat_user_tables
    WHERE n_dead_tup > 10000
    ORDER BY n_dead_tup DESC
    LIMIT 20;
    

  4. **PostgreSQL: check if autovacuum is keeping up**



    
    
    -- Tables where autovacuum is behind
    SELECT relname, last_autovacuum, autovacuum_count, n_dead_tup
    FROM pg_stat_user_tables
    WHERE last_autovacuum < NOW() - INTERVAL '1 day'
       OR last_autovacuum IS NULL
    ORDER BY n_dead_tup DESC
    LIMIT 10;
    

  5. **MySQL/MariaDB: find the largest tables**



    
    
    -- Connect: mysql -u root -p
    
    SELECT table_schema,
           table_name,
           ROUND((data_length + index_length) / 1024 / 1024, 1) AS mb
    FROM information_schema.tables
    ORDER BY data_length + index_length DESC
    LIMIT 20;
    

  6. **MySQL: check binary log accumulation**



    
    
    SHOW BINARY LOGS;
    -- Lists all retained bin log files and their sizes
    -- If the list is long and sizes are large, purge policy is missing or broken
    
    SHOW VARIABLES LIKE 'expire_logs_days';
    SHOW VARIABLES LIKE 'binlog_expire_logs_seconds';
    -- A value of 0 means binary logs are never automatically purged
    

  7. **Check for failed retention cleanup jobs**



    
    
    # Check cron job history
    grep -i "cleanup\|purge\|retain\|delete" /var/spool/cron/* /etc/cron.d/* 2>/dev/null
    
    # Check systemd timer status for database maintenance jobs
    systemctl list-timers | grep -i "vacuum\|cleanup\|purge\|pg\|mysql"
    
    # Check recent job execution
    journalctl --since "7 days ago" | grep -i "vacuum\|cleanup\|retention"
    

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Write credentials for the database| Required for VACUUM, REINDEX, DELETE, PURGE commands  
Data deletion must be reviewed against retention and compliance requirements| Deleting records prematurely may violate regulatory obligations (GDPR, HIPAA, SOX); confirm with the data owner  
For large DELETE operations: confirm application write load is low or can be quiesced| Large bulk deletes compete with application writes for lock access and can cause cascading slowdowns  
For REINDEX CONCURRENT: sufficient disk space must exist for the new index to be built alongside the old one| REINDEX CONCURRENT temporarily doubles index storage  
  
### Immediate Space Recovery

**PostgreSQL: run VACUUM to reclaim dead tuples**
    
    
    -- Standard VACUUM (runs online, does not lock the table)
    VACUUM ANALYZE <table_name>;
    
    -- VACUUM FULL (reclaims space to OS; requires exclusive lock - causes downtime on that table)
    -- Use only in a maintenance window
    VACUUM FULL <table_name>;
    

**PostgreSQL: rebuild a bloated index**
    
    
    -- Online rebuild (no table lock; takes longer)
    REINDEX INDEX CONCURRENTLY <index_name>;
    
    -- All indexes on a table (online)
    REINDEX TABLE CONCURRENTLY <table_name>;
    

**MySQL: purge binary logs**
    
    
    -- Remove bin logs older than 7 days
    PURGE BINARY LOGS BEFORE NOW() - INTERVAL 7 DAY;
    
    -- Or remove everything up to a specific log file
    PURGE BINARY LOGS TO 'mysql-bin.001234';
    

**Delete old data (application-specific):**
    
    
    -- Example: delete records older than 90 days in batches to avoid lock contention
    DELETE FROM events
    WHERE created_at < NOW() - INTERVAL '90 days'
    LIMIT 10000;
    -- Repeat in a loop with a short sleep between batches
    

### Permanent Fix

**MySQL: set binary log expiry:**
    
    
    -- In /etc/mysql/mysql.conf.d/mysqld.cnf
    [mysqld]
    binlog_expire_logs_seconds = 604800   -- 7 days
    

Apply with `systemctl reload mysql` (if supported) or a scheduled restart.

**PostgreSQL: tune autovacuum aggressiveness:**
    
    
    -- Per-table setting for a high-churn table
    ALTER TABLE <table_name>
      SET (autovacuum_vacuum_scale_factor = 0.01,
           autovacuum_analyze_scale_factor = 0.005);
    

**Add or fix the data retention job:**
    
    
    # Example cron entry for a nightly delete job
    0 2 * * * postgres psql -d <dbname> -c "DELETE FROM events WHERE created_at < NOW() - INTERVAL '90 days';" >> /var/log/db-cleanup.log 2>&1
    

**PostgreSQL: implement table partitioning** (for very high-volume tables): partition by time and drop old partitions instead of row-by-row DELETE. Dropping a partition is nearly instantaneous and produces no dead tuples.

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
`VACUUM ANALYZE`| None| Online operation; table remains readable and writable throughout  
`REINDEX INDEX CONCURRENTLY`| None| Online; takes longer than standard REINDEX but does not lock the table  
Batch DELETE (small batches with sleep)| Minimal; brief row-level locks per batch| Application may see slightly slower query response on the affected table during batch runs; use off-peak hours for large datasets  
`PURGE BINARY LOGS` (MySQL)| None| Only removes archived replication logs; no active sessions affected  
`VACUUM FULL`| **Table-level exclusive lock**|  The table is inaccessible for reads and writes for the duration (seconds to hours depending on table size); schedule a maintenance window  
Standard `REINDEX` (without CONCURRENTLY)| **Table-level lock**|  Same as VACUUM FULL; block all queries on the table; use CONCURRENTLY variant instead  
`systemctl reload mysql` (applying `binlog_expire_logs_seconds`)| None if reload is supported; otherwise restart required| Check if `mysql -e "SET GLOBAL binlog_expire_logs_seconds=604800;"` can apply it without a restart  
Adding table partitioning to an existing table| **Major migration**|  Requires locking and rewriting the table; plan as a dedicated migration with downtime or use `pg_partman` \+ logical replication for zero-downtime migration  
Fixing a failed retention job (editing cron)| None immediately| Only affects future scheduled runs  
  
**The two high-impact operations to avoid in production without a maintenance window are**`VACUUM FULL`**and non-concurrent**`REINDEX`**.** For all other cleanup steps, the database and its dependent services remain fully available.

* * *

## Verification
    
    
    # Confirm disk usage has dropped
    df -h <device>
    
    # PostgreSQL: confirm dead tuples have been cleared
    psql -U postgres -c "SELECT relname, n_dead_tup FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;"
    
    # MySQL: confirm binary logs have been purged
    mysql -u root -p -e "SHOW BINARY LOGS;"
    
    # In Datadog
    # avg:system.disk.in_use{host:<hostname>,device:<device>}
    # Should drop below warning threshold within 1-5 minutes of space being reclaimed
    

* * *

## Related Scenarios

  * If the disk fills completely before VACUUM can run, PostgreSQL may stop accepting writes (`could not write to file` errors). In this case, immediate space recovery (moving or deleting old binary logs, removing tmp files on the same partition) must happen first to allow VACUUM to proceed.

  * Slow queries surfaced in APM may persist even after dead tuples are removed if indexes were also bloated; follow with `REINDEX CONCURRENTLY`.

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6544857210 -->
