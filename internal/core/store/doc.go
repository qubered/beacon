// Package store is the Postgres layer: repositories, migrations, partition management and rollups.
//
// Decision D22: one relational database, no separate queue or time-series service. Every extra stateful service is another thing to back up, monitor and explain at 7pm. Tables are structured so a time-series extension is a later drop-in rather than a migration.
//
// Pre-create partitions ahead of time with a catch-all default. If rollover fails or the process is down at a boundary, every insert fails and the spool sheds everything. Expire by dropping partitions, not deleting rows. Partition boundaries are UTC; reporting days are the site's timezone, and they are different things.
//
// Decision D23: uptime is time-weighted from monitor_state_periods, never counted from runs. Report both figures, raw and excluding maintenance.
//
// Boot-time reconciliation after a restore: stale claims cleared, connection states reset, schedules reseeded, missed-run counters suppressed for the first pass — otherwise a restore pages someone about a hundred thousand missed runs.
package store
