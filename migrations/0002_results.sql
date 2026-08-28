-- 0002 results: runs, state periods, metrics, events, state.
--
-- Time-partitioned with a rollup path, structured so a time-series extension is
-- a later drop-in rather than a migration (D22).
--
-- Partitions are pre-created ahead of time WITH A CATCH-ALL DEFAULT. If rollover
-- fails, or the process is down at a boundary, every insert fails and the agent
-- spool sheds everything — which is a data-loss incident caused by a cron job.
-- Expire by dropping partitions, never by deleting rows.
--
-- Note that partition boundaries are UTC and reporting "days" are the site's
-- timezone. They are different things and must not be conflated.

CREATE TYPE run_status  AS ENUM ('up', 'degraded', 'down', 'unknown');
CREATE TYPE run_outcome AS ENUM ('ok', 'failed', 'throttled', 'skipped', 'error');

-- `protocol` is the important one: the device answered and we misread it. That
-- is a flow bug or a firmware change, not a gear fault, and after a firmware
-- update twelve monitors going 'protocol' at once is a completely different
-- message from twelve going 'timeout' (spec §6.2, §11).
CREATE TYPE error_class AS ENUM (
    'none', 'timeout', 'connect_refused', 'dns', 'tls', 'auth', 'protocol',
    'assertion', 'sandbox_timeout', 'sandbox_memory', 'agent_unreachable', 'internal'
);

CREATE TABLE monitor_runs (
    site_id      uuid NOT NULL,
    monitor_id   uuid NOT NULL,
    -- The scheduled slot, not the execution time. Doubles as the execution fence
    -- so at-least-once delivery deduplicates on insert rather than
    -- double-counting (spec §7.3).
    scheduled_at timestamptz NOT NULL,
    started_at   timestamptz NOT NULL,
    duration_ms  int NOT NULL,

    -- Without this you cannot explain a historical run once the flow is edited.
    flow_version_id uuid NOT NULL,
    attempt      int NOT NULL DEFAULT 0,

    status       run_status NOT NULL,
    outcome      run_outcome NOT NULL,
    error_class  error_class NOT NULL DEFAULT 'none',
    message      text,

    -- Stored fact, not alerting-layer logic: true only when status officially
    -- changes after retries exhaust. Makes alert history queryable and gives
    -- incidents a natural anchor (spec §11).
    is_transition boolean NOT NULL DEFAULT false,

    -- Per-node input/output. NULL when retention says no capture. Captures are
    -- dropped at write time, never written and pruned later (spec §8).
    capture      jsonb,
    capture_truncated boolean NOT NULL DEFAULT false,

    PRIMARY KEY (monitor_id, scheduled_at)
) PARTITION BY RANGE (scheduled_at);

CREATE TABLE monitor_runs_default PARTITION OF monitor_runs DEFAULT;
CREATE INDEX monitor_runs_transitions ON monitor_runs (monitor_id, scheduled_at DESC) WHERE is_transition;

-- The source of truth for uptime (D23). Time-weighted from these periods, never
-- counted from runs: change a monitor's interval and run-count uptime re-weights
-- yesterday's failures, and missed runs produce no row at all because the
-- scheduler snaps past them.
CREATE TABLE monitor_state_periods (
    id           bigserial,
    site_id      uuid NOT NULL,
    monitor_id   uuid NOT NULL,
    state        monitor_state NOT NULL,
    from_at      timestamptz NOT NULL,
    to_at        timestamptz,               -- NULL = current period
    in_maintenance boolean NOT NULL DEFAULT false,
    PRIMARY KEY (monitor_id, from_at)
) PARTITION BY RANGE (from_at);

CREATE TABLE monitor_state_periods_default PARTITION OF monitor_state_periods DEFAULT;

-- Backs delta/rate and previous-run access (spec §6.2).
CREATE TABLE monitor_last_values (
    site_id     uuid NOT NULL,
    monitor_id  uuid NOT NULL,
    key         text NOT NULL,
    value       jsonb NOT NULL,
    numeric_value double precision,
    recorded_at timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, key)
);

-- Metrics ---------------------------------------------------------------------

-- Unique per (monitor, name, labels) — NOT globally by label hash, or two
-- monitors emitting the same label set collide (spec §10).
CREATE TABLE metric_series (
    id          bigserial PRIMARY KEY,
    site_id     uuid NOT NULL,
    monitor_id  uuid NOT NULL,
    name        text NOT NULL,
    type        text NOT NULL,
    unit        text NOT NULL DEFAULT '',
    labels      jsonb NOT NULL DEFAULT '{}'::jsonb,
    label_hash  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (monitor_id, name, label_hash)
);

CREATE TABLE metric_samples (
    series_id   bigint NOT NULL,
    at          timestamptz NOT NULL,
    value       double precision NOT NULL,
    -- User-authored flows WILL emit duplicates. Tolerate them on insert and
    -- count, rather than rejecting the write (spec §10).
    duplicates  int NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, at)
) PARTITION BY RANGE (at);

CREATE TABLE metric_samples_default PARTITION OF metric_samples DEFAULT;

CREATE TABLE events (
    id          bigserial,
    site_id     uuid NOT NULL,
    at          timestamptz NOT NULL,
    monitor_id  uuid,
    device_id   uuid,
    agent_id    uuid,
    severity    text NOT NULL,
    kind        text NOT NULL,
    message     text NOT NULL,
    fields      jsonb NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (at, id)
) PARTITION BY RANGE (at);

CREATE TABLE events_default PARTITION OF events DEFAULT;
CREATE INDEX events_by_device ON events (device_id, at DESC);

-- State -----------------------------------------------------------------------

CREATE TABLE kv_state (
    site_id     uuid NOT NULL,
    scope       text NOT NULL,
    key         text NOT NULL,
    value       jsonb NOT NULL,
    expires_at  timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, scope, key)
);

-- Per connection, keyed. numeric_value is denormalised for fast threshold
-- evaluation by derived monitors, and the TTL stops keys a device stops sending
-- from accumulating forever (spec §10).
CREATE TABLE subscription_state (
    site_id       uuid NOT NULL,
    connection_id uuid NOT NULL,
    key           text NOT NULL,
    value         jsonb NOT NULL,
    numeric_value double precision,
    received_at   timestamptz NOT NULL,
    expires_at    timestamptz,
    PRIMARY KEY (connection_id, key)
);

CREATE INDEX subscription_state_expiry ON subscription_state (expires_at) WHERE expires_at IS NOT NULL;
