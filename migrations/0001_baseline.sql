-- 0001 baseline: identity, topology, credentials, flows, monitors.
--
-- Site scoping is enforced from here (D30, spec §19.7). Every table carries
-- site_id and every composite foreign key includes it, so a row can never
-- reference a parent in another site. That constraint is the enforcement; the
-- application layer's site.ID parameter is the ergonomics.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE sites (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    -- IANA name, never a fixed offset: a 2am maintenance window must not drift
    -- an hour at DST (spec §11).
    timezone    text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Agents ----------------------------------------------------------------------

CREATE TYPE agent_link_state AS ENUM ('never_seen', 'connected', 'disconnected', 'draining', 'revoked');
CREATE TYPE enrolment_state  AS ENUM ('pending', 'approved', 'rejected', 'revoked');

CREATE TABLE agents (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id           uuid NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,
    name              text NOT NULL,

    -- Enrolment waits for approval by default: "a small computer appeared on the
    -- network and joined the monitoring system" should be a decision, not an
    -- event (spec §16).
    enrolment_state   enrolment_state NOT NULL DEFAULT 'pending',
    public_key        bytea,
    cert_serial       text,
    cert_not_after    timestamptz,

    version           text,
    -- Node type -> implemented config schema version. Generated from the agent's
    -- node registry, so capability gating is a property of the build (spec §7.5).
    capabilities      jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- Digest only. The policy itself is authoritative on the agent and Core
    -- cannot widen it (I7, D17).
    egress_digest     text,
    write_nodes_enabled boolean NOT NULL DEFAULT false,
    multicast_enabled   boolean NOT NULL DEFAULT false,

    link_state        agent_link_state NOT NULL DEFAULT 'never_seen',
    -- Liveness is judged against Core's receipt time, never the agent's claimed
    -- timestamp, so a skewed agent cannot fake being alive (spec §7.7).
    last_seen_at      timestamptz,
    clock_skew_ms     bigint,
    spool_depth       bigint NOT NULL DEFAULT 0,

    is_local          boolean NOT NULL DEFAULT false,  -- Core's own agent (D13)
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    UNIQUE (site_id, name),
    UNIQUE (id, site_id)
);

CREATE UNIQUE INDEX agents_one_local_per_site ON agents (site_id) WHERE is_local;

CREATE TABLE enrolment_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id       uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    token_hash    bytea NOT NULL UNIQUE,
    intended_name text NOT NULL,
    egress_policy jsonb NOT NULL,
    expires_at    timestamptz NOT NULL,
    -- Single use: burned when Core issues the certificate.
    consumed_at   timestamptz,
    consumed_by   uuid REFERENCES agents(id) ON DELETE SET NULL,
    created_by    uuid,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Devices ---------------------------------------------------------------------

CREATE TYPE reachability AS ENUM ('unknown', 'reachable', 'unreachable_from_here', 'filtered');
CREATE TYPE device_health AS ENUM ('unknown', 'up', 'degraded', 'down');

CREATE TABLE devices (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id       uuid NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,

    -- Exactly one agent per device (I5). Two executors must never hold sockets to
    -- the same device, so this is a NOT NULL column rather than a join table.
    agent_id      uuid NOT NULL,

    name          text NOT NULL,
    host          text NOT NULL,
    tags          text[] NOT NULL DEFAULT '{}',
    -- Device vars are how one flow serves fourteen devices with different channel
    -- counts (spec §6.2).
    vars          jsonb NOT NULL DEFAULT '{}'::jsonb,

    max_concurrent_connections int NOT NULL DEFAULT 1,
    min_request_interval_ms    int NOT NULL DEFAULT 0,

    reachability      reachability NOT NULL DEFAULT 'unknown',
    -- Persisted worst-of rollup over monitors flagged as reachability checks, so
    -- an out-of-range battery reading never suppresses an entire rack (spec §11).
    health            device_health NOT NULL DEFAULT 'unknown',
    health_changed_at timestamptz,

    discovery_meta jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    UNIQUE (site_id, name),
    UNIQUE (id, site_id),
    FOREIGN KEY (agent_id, site_id) REFERENCES agents (id, site_id) ON DELETE RESTRICT
);

CREATE INDEX devices_by_agent ON devices (agent_id);
CREATE INDEX devices_by_tags  ON devices USING gin (tags);

-- A DAG, not a tree: AV racks have redundant paths and a single parent column
-- cannot express them (spec §10).
CREATE TABLE device_edges (
    site_id   uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    child_id  uuid NOT NULL,
    parent_id uuid NOT NULL,
    kind      text NOT NULL DEFAULT 'depends_on',
    source    text NOT NULL DEFAULT 'manual',  -- manual | discovered
    created_at timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (child_id, parent_id, kind),
    CHECK (child_id <> parent_id),
    FOREIGN KEY (child_id, site_id)  REFERENCES devices (id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id, site_id) REFERENCES devices (id, site_id) ON DELETE CASCADE
);

CREATE INDEX device_edges_by_parent ON device_edges (parent_id);

-- Credentials -----------------------------------------------------------------

CREATE TABLE credentials (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id      uuid NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,
    name         text NOT NULL,
    kind         text NOT NULL,
    -- Envelope encrypted. The master key is backed up SEPARATELY from this
    -- database, and never alongside it (docs/security.md).
    ciphertext   bytea NOT NULL,
    key_id       text NOT NULL,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),

    UNIQUE (site_id, name),
    UNIQUE (id, site_id)
);

-- Credentials are granted to the devices they belong to rather than being usable
-- from every flow. This is the mitigation for the honest limitation in §16: an
-- author can HMAC a secret and transmit the result, so the blast radius of a
-- given credential is bounded by grant rather than by the sealing mechanism.
CREATE TABLE credential_grants (
    site_id       uuid NOT NULL,
    credential_id uuid NOT NULL,
    device_id     uuid NOT NULL,
    PRIMARY KEY (credential_id, device_id),
    FOREIGN KEY (credential_id, site_id) REFERENCES credentials (id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (device_id, site_id)     REFERENCES devices (id, site_id)     ON DELETE CASCADE
);

-- Flows -----------------------------------------------------------------------

CREATE TABLE flows (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id     uuid NOT NULL REFERENCES sites(id) ON DELETE RESTRICT,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    category    text NOT NULL DEFAULT '',
    pack_id     uuid,
    -- Editing an installed Pack's flow forks it into the local library and marks
    -- it detached, with a merge prompt when the Pack updates (spec §14).
    detached_from_pack boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),

    UNIQUE (site_id, name),
    UNIQUE (id, site_id)
);

CREATE TABLE flow_versions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id       uuid NOT NULL,
    flow_id       uuid NOT NULL,
    version       int NOT NULL,
    -- Content-addressable, so agent-side caching is trivially correct (D28).
    content_hash  text NOT NULL,
    graph         jsonb NOT NULL,
    graph_schema_version int NOT NULL,
    -- Node type -> minimum config schema version. Compared against an agent's
    -- declared capabilities before assignment (spec §7.5).
    required_capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    egress_scope  jsonb NOT NULL DEFAULT '{}'::jsonb,
    has_write_nodes boolean NOT NULL DEFAULT false,
    changelog     text NOT NULL DEFAULT '',
    published_at  timestamptz,
    published_by  uuid,

    UNIQUE (flow_id, version),
    UNIQUE (id, site_id),
    FOREIGN KEY (flow_id, site_id) REFERENCES flows (id, site_id) ON DELETE CASCADE
);

CREATE INDEX flow_versions_by_hash ON flow_versions (content_hash);

-- I3: a published flow version is immutable. Editing forks a draft.
CREATE FUNCTION flow_versions_immutable() RETURNS trigger AS $$
BEGIN
    IF OLD.published_at IS NOT NULL THEN
        RAISE EXCEPTION 'flow version %/% is published and immutable (invariant I3); edit forks a draft',
            OLD.flow_id, OLD.version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER flow_versions_no_update
    BEFORE UPDATE OR DELETE ON flow_versions
    FOR EACH ROW EXECUTE FUNCTION flow_versions_immutable();

-- Monitors --------------------------------------------------------------------

CREATE TYPE monitor_mode  AS ENUM ('poll', 'session', 'derived');
CREATE TYPE monitor_state AS ENUM ('unknown', 'up', 'suspect', 'down', 'recovering');

CREATE TABLE monitors (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id     uuid NOT NULL,
    device_id   uuid NOT NULL,
    flow_id     uuid NOT NULL,
    -- Pinned version, or NULL to track the flow's latest published version.
    flow_version_id uuid,

    name        text NOT NULL,
    mode        monitor_mode NOT NULL DEFAULT 'poll',
    vars        jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled     boolean NOT NULL DEFAULT true,

    interval_ms int NOT NULL,
    timeout_ms  int NOT NULL,
    retries     int NOT NULL DEFAULT 0,
    retry_interval_ms int NOT NULL DEFAULT 0,

    -- Exactly one monitor per device should carry this, and it is what defines
    -- device health for dependency suppression (spec §11).
    is_reachability_check boolean NOT NULL DEFAULT false,
    alert_policy_id uuid,

    -- The persisted alert state machine. The recent-result ring lives here rather
    -- than being recomputed from the partitioned run table: at a thousand
    -- monitors that would be a thousand queries per cycle for a number that fits
    -- in a few dozen bytes (spec §11).
    state              monitor_state NOT NULL DEFAULT 'unknown',
    state_since        timestamptz,
    consecutive_failures int NOT NULL DEFAULT 0,
    consecutive_successes int NOT NULL DEFAULT 0,
    flap_percent       real NOT NULL DEFAULT 0,
    is_flapping        boolean NOT NULL DEFAULT false,
    recent_results     bytea NOT NULL DEFAULT '\x'::bytea,
    missed_runs        bigint NOT NULL DEFAULT 0,
    throttled_runs     bigint NOT NULL DEFAULT 0,

    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    UNIQUE (id, site_id),
    CHECK (interval_ms > 0 AND timeout_ms > 0),
    FOREIGN KEY (device_id, site_id) REFERENCES devices (id, site_id) ON DELETE CASCADE,
    FOREIGN KEY (flow_id, site_id)   REFERENCES flows (id, site_id)   ON DELETE RESTRICT,
    FOREIGN KEY (flow_version_id, site_id) REFERENCES flow_versions (id, site_id) ON DELETE RESTRICT
);

CREATE INDEX monitors_by_device ON monitors (device_id);
CREATE INDEX monitors_by_flow   ON monitors (flow_id);
