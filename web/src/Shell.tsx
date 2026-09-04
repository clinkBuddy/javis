import { NavLink, Outlet } from "react-router-dom";

import { usePolled } from "./hooks";
import { api } from "./api";

export function Shell() {
  // The header doubles as a liveness indicator: if JARVIS stops answering,
  // this is where it shows, rather than every page failing separately.
  const health = usePolled(api.health, 10_000);

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark">J</span>
          <span className="brand-name">JARVIS</span>
        </div>

        <nav className="nav">
          <NavLink to="/" end>
            앱
          </NavLink>
          <NavLink to="/jdks">JDK</NavLink>
        </nav>

        <div className="topbar-meta">
          {health.data ? (
            <>
              <span className="meta-item" title={health.data.dataRoot}>
                {health.data.version}
              </span>
              <span className="meta-item">가동 {health.data.uptime}</span>
              <span className={health.data.database === "ok" ? "dot dot-ok" : "dot dot-bad"} />
            </>
          ) : (
            <span className="meta-item meta-warn">
              {health.error ? "연결 끊김" : "연결 중…"}
            </span>
          )}
        </div>
      </header>

      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
