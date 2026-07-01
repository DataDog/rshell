# Memory - Long-Running Database Transaction

**Signal:** `pg_stat_activity` showing transactions open for minutes or hours; WAL directory size growing; `system.disk.in_use` rising on the PostgreSQL data volume  
**IssueType:** `memory_usage`  
**Metric (typical):** `postgresql.queries.count`, `system.disk.in_use` (PostgreSQL data volume)

* * *

## Command Reference

Tool| Stage| Examples  
---|---|---  
`psql`| both| `SELECT ... FROM pg_stat_activity` · `SELECT pg_terminate_backend(pid)` · `SELECT pg_cancel_backend(pid)`  
`du`| investigation| `du -sh $PGDATA/pg_wal/`  
`ls`| investigation| `ls -lh $PGDATA/pg_wal/ \| wc -l`  
`grep`| investigation| `grep -E "idle_in_transaction_session_timeout\|statement_timeout\|lock_timeout" /etc/postgresql/*/*/postgresql.conf`  
`cat`| investigation| `cat /proc/<PID>/status \| grep VmRSS`  
`df`| investigation| `df -h $PGDATA`  
  
* * *

## What Happens

A PostgreSQL transaction that stays open for an extended period holds a snapshot of the database state from the moment it began. While the transaction is open:

  * **VACUUM is blocked** : PostgreSQL cannot reclaim dead tuples (rows deleted or updated after the transaction started) because the open transaction still needs to see the old versions. Dead tuples accumulate, causing table bloat — the physical table file grows even though the live row count is stable.

  * **WAL grows** : PostgreSQL must retain Write-Ahead Log segments that the open transaction may still need, preventing WAL archival or recycling. On a busy database, WAL can grow to tens of gigabytes within hours.

  * **Locks are held** : If the transaction acquired row or table locks, all other queries waiting on those locks are blocked. Blocked queries accumulate in `pg_stat_activity`, consuming connection slots and memory.

  * **Replication can lag** : On replicas, recovery is paused at the WAL position the primary is waiting to archive; replication lag grows.




Common causes:

  * An application opened a transaction and then stalled (waiting on an external API, a slow computation, or network I/O) without setting a statement or idle-in-transaction timeout

  * A client disconnected mid-transaction without rolling back — PostgreSQL holds the transaction open until it detects the TCP connection is gone (which can take minutes depending on TCP keepalive settings)

  * A batch migration or export job wrapped too many rows in a single transaction

  * An ORM framework that wraps every request in a transaction, combined with a long-running HTTP handler

  * A forgotten `BEGIN` in an interactive `psql` session left open on a developer's laptop




* * *

## Detection

The platform detects this via elevated `system.disk.in_use` on the PostgreSQL data volume (WAL growth) or via a PostgreSQL integration monitor on query age. The WAL growth signal is typically the first OS-level indicator; by the time it appears, the transaction has usually been open for tens of minutes or more.

**Correlated signals to check:**

  * `system.disk.in_use` rising on the volume hosting `$PGDATA` — specifically the `pg_wal/` subdirectory

  * `postgresql.connections` at or near `max_connections` — blocked queries pile up consuming connection slots

  * Application error spans mentioning `could not obtain lock`, `deadlock detected`, or `connection pool exhausted`

  * Replication lag metric (`postgresql.replication.delay`) growing on standbys




* * *

## Investigation

### Preconditions

Precondition| Rationale  
---|---  
SSH or remote exec access to the host| Required to inspect WAL directory size and run OS-level commands  
Read-only DB access (`pg_monitor` role or equivalent)| Sufficient for all investigation queries (`pg_stat_activity`, `pg_locks`, `SHOW ...`). Connect with: `psql -h <host> -U <monitor_user> -d postgres`. If no dedicated monitoring user exists, use `sudo -u postgres psql` on the host  
Know the value of `$PGDATA`| Needed to find the WAL directory; default is `/var/lib/postgresql/<version>/main` on Debian/Ubuntu. Retrieve it with `SHOW data_directory;` if unknown  
  
### Steps

  1. **Check WAL directory size**



    
    
    # Retrieve PGDATA if not already known
    sudo -u postgres psql -c "SHOW data_directory;"
    
    # WAL size
    du -sh $PGDATA/pg_wal/
    ls -lh $PGDATA/pg_wal/ | wc -l   # number of WAL segment files; each is typically 16 MB
    
    # Overall data volume headroom
    df -h $PGDATA

  2. **Find long-running transactions**



    
    
    -- All transactions open longer than 5 minutes, oldest first
    SELECT pid,
           usename,
           application_name,
           client_addr,
           state,
           now() - xact_start AS transaction_age,
           now() - query_start AS query_age,
           left(query, 120)    AS current_query
    FROM pg_stat_activity
    WHERE xact_start IS NOT NULL
      AND now() - xact_start > interval '5 minutes'
    ORDER BY transaction_age DESC;

  3. **Check for idle-in-transaction sessions**



    
    
    -- Sessions in "idle in transaction" state (transaction open, no active query)
    -- These are the most dangerous: they hold a snapshot indefinitely with no CPU cost
    SELECT pid, usename, application_name, client_addr,
           state,
           now() - xact_start AS open_for,
           now() - state_change AS idle_for
    FROM pg_stat_activity
    WHERE state = 'idle in transaction'
    ORDER BY open_for DESC;

  4. **Check what the long-running transaction is blocking**



    
    
    -- Queries waiting on locks held by another session
    SELECT blocked.pid        AS blocked_pid,
           blocked.query      AS blocked_query,
           blocking.pid       AS blocking_pid,
           blocking.query     AS blocking_query,
           now() - blocked.query_start AS waiting_for
    FROM pg_stat_activity AS blocked
    JOIN pg_stat_activity AS blocking
      ON blocking.pid = ANY(pg_blocking_pids(blocked.pid))
    WHERE cardinality(pg_blocking_pids(blocked.pid)) > 0
    ORDER BY waiting_for DESC;

  5. **Identify the oldest transaction horizon (xmin)**



    
    
    -- The oldest transaction ID still active — determines how far back VACUUM must retain dead tuples
    SELECT min(age(backend_xmin)) AS oldest_xmin_age,
           min(age(xact_start::text::xid)) AS oldest_xact_age
    FROM pg_stat_activity
    WHERE backend_xmin IS NOT NULL;
    
    -- Table bloat indicator: tables with many dead tuples
    SELECT relname, n_dead_tup, n_live_tup,
           round(n_dead_tup::numeric / NULLIF(n_live_tup + n_dead_tup, 0) * 100, 1) AS dead_pct,
           last_autovacuum
    FROM pg_stat_user_tables
    WHERE n_dead_tup > 10000
    ORDER BY n_dead_tup DESC
    LIMIT 15;

  6. **Check current timeout settings**



    
    
    SHOW statement_timeout;
    SHOW idle_in_transaction_session_timeout;
    SHOW lock_timeout;
    -- An empty string or '0' means the timeout is disabled

* * *

## Remediation

### Preconditions

Precondition| Rationale  
---|---  
Superuser or `pg_signal_backend` role for termination| `pg_cancel_backend` and `pg_terminate_backend` require superuser, ownership of the target session, or the `pg_signal_backend` role (PostgreSQL 14+). Both functions return `false` — with no error — if the caller lacks privileges, so verify your access before relying on them. Use `sudo -u postgres psql` if no elevated role is available  
Superuser for timeout changes| `ALTER SYSTEM SET` and `ALTER ROLE ... SET` require superuser or `CREATEROLE`. Read-only monitoring users cannot make these changes  
Confirm the transaction is not part of a critical in-flight operation| Terminating a transaction rolls it back entirely; confirm with the application owner if the transaction was performing a migration or bulk write  
Use `pg_cancel_backend` before `pg_terminate_backend` for active queries| `pg_cancel_backend` sends SIGINT (interrupts the current query but leaves the connection); `pg_terminate_backend` sends SIGTERM (closes the connection and rolls back) — try the softer option first  
Timeout changes apply to new sessions only| Changes via `ALTER SYSTEM` \+ `SELECT pg_reload_conf()` apply to future connections; existing long-running sessions are not immediately affected  
  
### Immediate Relief

**Connect as a user with termination privileges:**
    
    
    sudo -u postgres psql
    -- or: psql -h <host> -U <superuser_or_pg_signal_backend_user> -d postgres

**Cancel the active query without closing the connection (softer; the client can retry):**
    
    
    SELECT pg_cancel_backend(<pid>);
    -- Returns true if the signal was sent, false if permission denied or pid not found

**Terminate the session and roll back its transaction (closes the connection):**
    
    
    SELECT pg_terminate_backend(<pid>);

**Terminate all idle-in-transaction sessions older than 10 minutes:**
    
    
    SELECT pg_terminate_backend(pid)
    FROM pg_stat_activity
    WHERE state = 'idle in transaction'
      AND now() - xact_start > interval '10 minutes';

**After terminating the blocking transaction, run VACUUM on the most bloated tables:**
    
    
    -- Online VACUUM (does not lock the table)
    VACUUM ANALYZE <table_name>;

### Set Timeouts to Prevent Recurrence

**Apply globally via**`postgresql.conf` (requires superuser):
    
    
    -- Terminate any query running longer than 30 minutes
    ALTER SYSTEM SET statement_timeout = '30min';
    
    -- Terminate connections idle in transaction for more than 5 minutes
    ALTER SYSTEM SET idle_in_transaction_session_timeout = '5min';
    
    -- Abort any statement waiting more than 30 seconds to acquire a lock
    ALTER SYSTEM SET lock_timeout = '30s';
    
    -- Reload config (no restart required)
    SELECT pg_reload_conf();

**Apply to a specific role only (less disruptive rollout):**
    
    
    ALTER ROLE <app_role> SET idle_in_transaction_session_timeout = '5min';
    ALTER ROLE <app_role> SET statement_timeout = '30min';

**Reclaim WAL space after the blocking transaction is gone:**

WAL recycling happens automatically once the oldest active transaction advances. Confirm WAL is shrinking:
    
    
    # Poll WAL directory size every 30 seconds
    while true; do
      echo "$(date +%T)  WAL=$(du -sh $PGDATA/pg_wal/ | cut -f1)"
      sleep 30
    done

* * *

## Service Impact During Remediation

Action| Service disruption| Notes  
---|---|---  
Querying `pg_stat_activity`, `pg_locks`| None| Read-only; no locks taken  
`pg_cancel_backend(<pid>)`| **Current query fails with error**|  The client receives `ERROR: canceling statement due to user request`; the connection stays open; the application can retry  
`pg_terminate_backend(<pid>)`| **Connection closed; transaction rolled back**|  The client receives a connection reset; all work in the transaction is lost; the application must reconnect and retry  
`VACUUM ANALYZE`| None| Online; table remains fully accessible  
`ALTER SYSTEM SET ... + pg_reload_conf()`| None| Applies to new connections only; existing sessions are unaffected until they reconnect  
`ALTER ROLE ... SET ...`| None| Same as above; takes effect on next login for that role  
  
`pg_terminate_backend` is the primary risk. Any uncommitted writes in the terminated transaction are lost. For a transaction that was performing a bulk migration or multi-row insert, this means the work must be redone. Always confirm the transaction's purpose before terminating.

* * *

## Verification
    
    
    -- Confirm no long-running transactions remain
    SELECT pid, state, now() - xact_start AS age
    FROM pg_stat_activity
    WHERE xact_start IS NOT NULL
      AND now() - xact_start > interval '5 minutes';
    -- Should return 0 rows
    
    -- Confirm no idle-in-transaction sessions remain
    SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction';
    
    -- Confirm WAL is shrinking
    -- du -sh $PGDATA/pg_wal/   (run twice 60 s apart; size should be stable or decreasing)
    
    -- Confirm timeouts are now set
    SHOW idle_in_transaction_session_timeout;
    SHOW statement_timeout;

In Datadog, verify:

  * `system.disk.in_use` on the PostgreSQL data volume levels off or drops as WAL is recycled

  * `postgresql.connections` returns to normal levels as blocked queries are unblocked

  * Application error spans mentioning lock waits or pool exhaustion cease




* * *

## Related Scenarios

  * If table bloat is severe after a long-running transaction, dead tuples do not disappear immediately after VACUUM — they are marked for reuse but the physical file does not shrink unless `VACUUM FULL` is run (which takes an exclusive lock); see the Database Growth scenario for guidance on bloat remediation.

  * If the WAL directory filled the disk completely before the transaction was terminated, PostgreSQL may have paused writes with `PANIC: could not write to file`; immediate space recovery (deleting other files on the volume) is required before the database can resume.

  * If `pg_stat_activity` shows many sessions in `idle in transaction` from the same `application_name`, the connection pool or ORM is not committing transactions promptly; the fix is application-side (`idle_in_transaction_session_timeout` provides a safety net but does not address the root cause).

<!-- Source: https://datadoghq.atlassian.net/wiki/spaces/IFREXP/pages/6866043043 -->
