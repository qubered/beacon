# Migrations

Numbered, forward-only SQL. Applied by `beaconctl migrate`.

Two rules that are easy to get wrong here:

**Every table carries `site_id`, and every composite foreign key includes it**
(D30). That is what makes a cross-site reference impossible at the database level
rather than merely unlikely at the application level.

**Partitions are pre-created ahead of time with a catch-all `DEFAULT` partition.**
If rollover fails, or the process happens to be down at a boundary, every insert
fails — and when inserts fail the agent spool fills and sheds. A missing partition
is therefore a data-loss incident caused by a cron job, which is why the default
partition exists as a backstop rather than as tidiness.

Expire data by **dropping partitions**, never by deleting rows.
