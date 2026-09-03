import { useState } from "react";
import { StatusWall } from "./StatusWall";
import { DeviceList } from "./DeviceList";

type Tab = "status" | "devices";

export function App() {
  const [tab, setTab] = useState<Tab>("status");

  return (
    <main
      style={{
        fontFamily: "system-ui, sans-serif",
        padding: "1.5rem",
        maxWidth: "72rem",
        margin: "0 auto",
        color: "var(--text)",
      }}
    >
      <header style={{ display: "flex", alignItems: "baseline", gap: "1rem" }}>
        <h1 style={{ fontSize: "1.25rem", margin: 0 }}>Beacon</h1>
        <nav style={{ display: "flex", gap: "0.25rem" }}>
          <TabButton active={tab === "status"} onClick={() => setTab("status")}>
            Status
          </TabButton>
          <TabButton active={tab === "devices"} onClick={() => setTab("devices")}>
            Devices
          </TabButton>
        </nav>
      </header>

      {/* Read-only, deliberately: writes arrive with authentication and the
          audit log, and a write route before those exist is a hole. */}
      <section style={{ marginTop: "1.25rem" }}>
        {tab === "status" ? <StatusWall /> : <DeviceList />}
      </section>
    </main>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      aria-current={active ? "page" : undefined}
      style={{
        border: "none",
        background: "none",
        padding: "0.25rem 0.5rem",
        cursor: "pointer",
        fontSize: "0.95rem",
        color: active ? "var(--link)" : "var(--text-muted)",
        borderBottom: active ? "2px solid var(--link)" : "2px solid transparent",
      }}
    >
      {children}
    </button>
  );
}
