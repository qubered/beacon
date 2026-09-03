import { useEffect, useState } from "react";
import { api, formatAge, isStale, type MonitorStatus } from "./api";

// Degraded is a state in its own right, not a shade of down, and it gets its
// own colour (spec §15). Unknown is deliberately grey rather than red: "we
// could not tell" is not a fault, and colouring it as one trains people to
// ignore red.
const STATE_COLOR: Record<string, string> = {
  up: "var(--state-up)",
  degraded: "var(--state-degraded)",
  down: "var(--state-down)",
  suspect: "var(--state-suspect)",
  recovering: "var(--state-recovering)",
  unknown: "var(--state-unknown)",
};

export function StatusWall() {
  const [monitors, setMonitors] = useState<MonitorStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    const load = () =>
      api
        .monitors()
        .then((r) => live && (setMonitors(r.monitors), setError(null)))
        .catch((e) => live && setError(String(e.message ?? e)));

    load();
    // The wall is the screen people leave open in the rack room, so it
    // refreshes itself. Ten seconds is well under the five-second interval
    // floor's worst case without being a poll storm.
    const timer = setInterval(load, 10_000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, []);

  if (error) return <Problem message={error} />;
  if (!monitors) return <p style={{ color: "var(--text-muted)" }}>Loading…</p>;
  if (monitors.length === 0) {
    return (
      <p style={{ color: "var(--text-muted)" }}>
        No monitors yet. Monitors are created against a device and a published flow.
      </p>
    );
  }

  return (
    <div
      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(15rem, 1fr))",
        gap: "0.75rem",
      }}
    >
      {monitors.map((m) => (
        <Tile key={m.id} monitor={m} />
      ))}
    </div>
  );
}

function Tile({ monitor: m }: { monitor: MonitorStatus }) {
  const stale = isStale(m);
  const color = STATE_COLOR[m.state] ?? STATE_COLOR.unknown;

  return (
    <article
      style={{
        border: "1px solid var(--border)",
        borderLeft: `4px solid ${color}`,
        borderRadius: "6px",
        padding: "0.75rem",
        background: m.enabled ? "var(--surface)" : "var(--surface-muted)",
        opacity: m.enabled ? 1 : 0.7,
      }}
    >
      <div style={{ display: "flex", justifyContent: "space-between", gap: "0.5rem" }}>
        <strong style={{ fontSize: "0.95rem" }}>{m.name}</strong>
        <span style={{ color, fontWeight: 600, fontSize: "0.85rem" }}>{m.state}</span>
      </div>
      <div style={{ color: "var(--text-muted)", fontSize: "0.85rem", marginTop: "0.15rem" }}>
        {m.device_name}
      </div>

      <div style={{ marginTop: "0.5rem", fontSize: "0.8rem", color: "var(--text-muted)" }}>
        {/* Last run is shown on every tile, not only failing ones: it is what
            separates "the check failed" from "the platform stopped checking". */}
        <div>
          last run {formatAge(m.last_run_at)}
          {stale && (
            <span style={{ color: "var(--state-degraded)", fontWeight: 600 }}>
              {" "}
              · stale
            </span>
          )}
        </div>

        {/* Flapping is a first-class number rather than something buried: a
            monitor oscillating is a different problem from one that is down. */}
        {m.is_flapping && (
          <div style={{ color: "var(--state-suspect)", fontWeight: 600 }}>
            flapping · {Math.round(m.flap_percent)}%
          </div>
        )}

        {/* A non-zero count here means the platform stopped collecting, or
            someone over-scheduled the device — both of which look like a
            healthy monitor if you only read the state. */}
        {m.missed_runs > 0 && <div>{m.missed_runs} missed</div>}
        {m.throttled_runs > 0 && <div>{m.throttled_runs} throttled</div>}

        {m.error_class && m.error_class !== "none" && (
          <div title={m.message ?? ""} style={{ color: "var(--state-down)" }}>
            {m.error_class}
          </div>
        )}
        {!m.enabled && <div>disabled</div>}
      </div>
    </article>
  );
}

export function Problem({ message }: { message: string }) {
  return (
    <div
      role="alert"
      style={{
        border: "1px solid var(--state-down)",
        borderRadius: "6px",
        padding: "0.75rem",
        color: "var(--state-down)",
      }}
    >
      {message}
    </div>
  );
}
