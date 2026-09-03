// The read-only client for Core's M3 API.
//
// These types are hand-written for now. The type mirror described in
// docs/architecture.md generates the *frame type* table from Go, because two
// hand-maintained copies of a validation table diverge in the direction of the
// editor permitting what the runtime rejects. These view models are a different
// case: they are a rendering concern, and a mismatch shows up as an empty cell
// rather than as a flow that fails at 6pm.

export type MonitorState =
  | "unknown"
  | "up"
  | "degraded"
  | "down"
  | "suspect"
  | "recovering";

export interface DeviceSummary {
  id: string;
  name: string;
  host: string;
  tags: string[];
  health: string;
  // Separate from health on purpose: a device can be reachable and unhealthy —
  // it answered, and a reading was out of range.
  reachability: string;
  agent_id: string;
  monitor_count: number;
  health_since?: string;
}

export interface MonitorStatus {
  id: string;
  name: string;
  device_id: string;
  device_name: string;
  state: MonitorState;
  state_since?: string;
  enabled: boolean;
  flap_percent: number;
  is_flapping: boolean;
  interval_ms: number;
  missed_runs: number;
  throttled_runs: number;
  // What distinguishes "the check failed" from "the platform stopped
  // checking". Without it a stale green tile looks exactly like a fresh one.
  last_run_at?: string;
  last_status?: string;
  error_class?: string;
  message?: string;
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      if (body?.error) detail = body.error;
    } catch {
      // The body was not JSON; the status line is all we have.
    }
    throw new Error(detail);
  }
  return res.json() as Promise<T>;
}

export const api = {
  devices: () => get<{ devices: DeviceSummary[] }>("/api/v1/devices"),
  monitors: () => get<{ monitors: MonitorStatus[] }>("/api/v1/monitors"),
};

/**
 * Whether a monitor's last run is old enough that its tile should not be
 * trusted at face value.
 *
 * A monitor whose agent stopped reporting keeps whatever state it last had, so
 * a green tile can be arbitrarily stale. Staleness is judged against the
 * monitor's own interval rather than a fixed threshold — a 5-second monitor
 * silent for two minutes is broken, while an hourly one is merely between runs.
 */
export function isStale(m: MonitorStatus, now: Date = new Date()): boolean {
  if (!m.last_run_at) return true;
  const age = now.getTime() - new Date(m.last_run_at).getTime();
  return age > Math.max(m.interval_ms * 3, 30_000);
}

export function formatAge(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) return "never";
  const secs = Math.max(0, Math.round((now.getTime() - new Date(iso).getTime()) / 1000));
  if (secs < 60) return `${secs}s ago`;
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`;
  return `${Math.floor(secs / 86400)}d ago`;
}
