import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";

import { usePolled } from "./hooks";
import { api } from "./api";
import { useSession } from "./session";
import { Login } from "./pages/Login";
import { ChangePassword } from "./pages/ChangePassword";

const MOBILE_MQ = "(max-width: 760px)";

function useMobile() {
  const [mobile, setMobile] = useState(() =>
    typeof window !== "undefined" ? window.matchMedia(MOBILE_MQ).matches : false,
  );
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_MQ);
    const onChange = () => setMobile(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);
  return mobile;
}

export function Shell() {
  const { user, ready } = useSession();
  const mobile = useMobile();

  if (!ready) {
    return <div className="login-screen muted">불러오는 중…</div>;
  }
  if (!user) {
    return <Login />;
  }
  if (user.mustChange) {
    return <ChangePassword forced />;
  }

  return (
    <div className={mobile ? "shell shell-mobile" : "shell"}>
      {mobile ? <MobileChrome /> : <Topbar />}
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}

function Topbar() {
  const { user, can, signOut } = useSession();
  const health = usePolled(api.health, 10_000);

  return (
    <header className="topbar">
      <div className="brand">
        <span className="brand-mark">J</span>
        <span className="brand-name">JARVIS</span>
      </div>

      <nav className="nav">
        <NavLink to="/" end>
          모니터링
        </NavLink>
        <NavLink to="/jdks">JDK</NavLink>
        {can("admin") && <NavLink to="/users">사용자</NavLink>}
        {can("admin") && <NavLink to="/bans">차단 IP</NavLink>}
      </nav>

      <div className="topbar-meta">
        <HealthBits health={health} />
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

function navItemClass({ isActive }: { isActive: boolean }) {
  return isActive ? "bottom-nav-item active" : "bottom-nav-item";
}

function mobileTitle(pathname: string): string {
  if (pathname === "/" || pathname.startsWith("/apps/")) return "모니터링";
  if (pathname.startsWith("/jdks")) return "JDK";
  if (pathname.startsWith("/users")) return "사용자";
  if (pathname.startsWith("/bans")) return "차단 IP";
  if (pathname.startsWith("/account")) return "내 계정";
  return "JARVIS";
}

function MobileChrome() {
  const { user, can, signOut } = useSession();
  const health = usePolled(api.health, 10_000);
  const location = useLocation();
  const [menuOpen, setMenuOpen] = useState(false);
  const adminSection =
    location.pathname.startsWith("/users") || location.pathname.startsWith("/bans");
  const monitorActive = location.pathname === "/" || location.pathname.startsWith("/apps/");

  useEffect(() => {
    setMenuOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    if (!menuOpen) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [menuOpen]);

  return (
    <>
      <header className="topbar topbar-mobile">
        <div className="brand">
          <span className="brand-mark">J</span>
          <span className="brand-name">{mobileTitle(location.pathname)}</span>
        </div>
        <div className="topbar-meta">
          {health.data ? (
            <span className={health.data.database === "ok" ? "dot dot-ok" : "dot dot-bad"} />
          ) : (
            <span className="dot dot-bad" />
          )}
        </div>
      </header>

      <nav className="bottom-nav" aria-label="모바일 메뉴">
        <NavLink
          to="/"
          end
          className={() => navItemClass({ isActive: monitorActive })}
        >
          <NavGlyph name="monitor" />
          모니터
        </NavLink>
        <NavLink to="/jdks" className={navItemClass}>
          <NavGlyph name="jdk" />
          JDK
        </NavLink>
        <NavLink to="/account" className={navItemClass}>
          <NavGlyph name="account" />
          계정
        </NavLink>
        <button
          type="button"
          className={navItemClass({ isActive: menuOpen || adminSection })}
          aria-expanded={menuOpen}
          aria-haspopup="dialog"
          onClick={() => setMenuOpen((v) => !v)}
        >
          <NavGlyph name="menu" />
          메뉴
        </button>
      </nav>

      {menuOpen && (
        <div className="mobile-sheet-backdrop" onClick={() => setMenuOpen(false)}>
          <div
            className="mobile-sheet"
            onClick={(e) => e.stopPropagation()}
            role="dialog"
            aria-label="더보기"
          >
            <div className="mobile-sheet-handle" />
            <p className="muted small mobile-sheet-user">
              {user?.username}
              <span className="pill">{user?.role}</span>
            </p>
            <div className="mobile-sheet-health">
              <HealthBits health={health} />
            </div>
            {can("admin") && (
              <>
                <NavLink to="/users" className="mobile-sheet-link" onClick={() => setMenuOpen(false)}>
                  사용자
                </NavLink>
                <NavLink to="/bans" className="mobile-sheet-link" onClick={() => setMenuOpen(false)}>
                  차단 IP
                </NavLink>
              </>
            )}
            <button
              className="btn btn-block"
              onClick={() => {
                setMenuOpen(false);
                void signOut();
              }}
            >
              로그아웃
            </button>
          </div>
        </div>
      )}
    </>
  );
}

function NavGlyph({ name }: { name: "monitor" | "jdk" | "account" | "menu" }) {
  return (
    <svg className="bottom-nav-icon" viewBox="0 0 24 24" aria-hidden="true">
      {name === "monitor" && (
        <>
          <rect x="3" y="4" width="18" height="13" rx="2" fill="none" stroke="currentColor" strokeWidth="2" />
          <path d="M8 21h8M12 17v4" fill="none" stroke="currentColor" strokeWidth="2" />
        </>
      )}
      {name === "jdk" && (
        <path
          d="M7 20V8l5-3 5 3v12H7zm5-9v6"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinejoin="round"
        />
      )}
      {name === "account" && (
        <>
          <circle cx="12" cy="8" r="3.5" fill="none" stroke="currentColor" strokeWidth="2" />
          <path d="M5 19c1.5-3.2 4-5 7-5s5.5 1.8 7 5" fill="none" stroke="currentColor" strokeWidth="2" />
        </>
      )}
      {name === "menu" && (
        <path d="M5 7h14M5 12h14M5 17h14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      )}
    </svg>
  );
}

function HealthBits({
  health,
}: {
  health: { data?: { version: string; uptime: string; database: string; dataRoot: string }; error?: string };
}) {
  if (!health.data) {
    return <span className="meta-item meta-warn">{health.error ? "연결 끊김" : "연결 중…"}</span>;
  }
  return (
    <>
      <span className="meta-item" title={health.data.dataRoot}>
        {health.data.version}
      </span>
      <span className="meta-item">가동 {health.data.uptime}</span>
      <span className={health.data.database === "ok" ? "dot dot-ok" : "dot dot-bad"} />
    </>
  );
}
