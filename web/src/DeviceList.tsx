import { useEffect, useState } from "react";
import { api, type DeviceSummary } from "./api";
import { Problem } from "./StatusWall";

export function DeviceList() {
  const [devices, setDevices] = useState<DeviceSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .devices()
      .then((r) => live && setDevices(r.devices))
      .catch((e) => live && setError(String(e.message ?? e)));
    return () => {
      live = false;
    };
  }, []);

  if (error) return <Problem message={error} />;
  if (!devices) return <p style={{ color: "var(--text-muted)" }}>Loading…</p>;
  if (devices.length === 0) {
    return <p style={{ color: "var(--text-muted)" }}>No devices yet.</p>;
  }

  return (
    // Wide content scrolls inside its own container rather than making the page
    // scroll sideways.
    <div style={{ overflowX: "auto" }}>
      <table style={{ borderCollapse: "collapse", width: "100%", fontSize: "0.9rem" }}>
        <thead>
          <tr style={{ textAlign: "left", borderBottom: "2px solid var(--border)" }}>
            <Th>Device</Th>
            <Th>Host</Th>
            <Th>Health</Th>
            {/* Reachability is its own column, not merged into health: a device
                can be reachable and unhealthy, and that distinction is what
                tells an operator whether to walk to the rack. */}
            <Th>Reachability</Th>
            <Th>Monitors</Th>
            <Th>Tags</Th>
          </tr>
        </thead>
        <tbody>
          {devices.map((d) => (
            <tr key={d.id} style={{ borderBottom: "1px solid var(--border-subtle)" }}>
              <Td>{d.name}</Td>
              <Td>
                <code>{d.host}</code>
              </Td>
              <Td>{d.health}</Td>
              <Td>{d.reachability}</Td>
              <Td>{d.monitor_count}</Td>
              <Td>{d.tags.join(", ")}</Td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return <th style={{ padding: "0.4rem 0.6rem", fontWeight: 600 }}>{children}</th>;
}

function Td({ children }: { children: React.ReactNode }) {
  return <td style={{ padding: "0.4rem 0.6rem" }}>{children}</td>;
}
