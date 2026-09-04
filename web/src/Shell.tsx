import { NavLink, Outlet } from "react-router-dom";

import { usePolled } from "./hooks";
import { api } from "./api";
import { useSession } from "./session";
import { Login } from "./pages/Login";
import { ChangePassword } from "./pages/ChangePassword";

export function Shell() {
  const { user, ready } = useSession();

  // Hold the first render until /auth/me settles, so someone who is already
  // signed in never sees the login form flash past.
  if (!ready) {
    return <div className="login-screen muted">불러오는 중…</div>;
  }
  if (!user) {
    return <Login />;
  }
  // The server refuses every other endpoint while must_change is set, so
  // rendering the app around this form would just produce a wall of 403s.
  if (user.mustChange) {
    return <ChangePassword forced />;
  }

  return (
    <div className="shell">
      <Topbar />

      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}

function Topbar() {
  const { user, can, signOut } = useSession();
  // The header doubles as a liveness indicator: if JARVIS stops answering,
  // this is where it shows, rather than every page failing separately.
  const health = usePolled(api.health, 10_000);

  return (
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
        {can("admin") && <NavLink to="/users">사용자</NavLink>}
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

        <span className="topbar-divider" />

        <NavLink to="/account" className="meta-item meta-link" title={user?.role}>
          {user?.username}
        </NavLink>
        <button className="btn btn-sm" onClick={() => void signOut()}>
          로그아웃
        </button>
      </div>
    </header>
  );
}
