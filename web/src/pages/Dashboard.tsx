import { useState } from "react";
import { Link } from "react-router-dom";

import {
  api,
  formatBytes,
  formatDuration,
  type App,
  type HostReading,
  type HTTPExchange,
  type ProcessReading,
  type TrafficSnapshot,
} from "../api";
import { useAction, usePolled } from "../hooks";
import { useSession } from "../session";
import { StateBadge } from "../components/StateBadge";
import { Sparkline } from "../components/Sparkline";
import { ExchangeTable, formatMs } from "../components/TrafficPanel";
import { XView } from "../components/XView";

export function Dashboard() {
  const apps = usePolled(api.listApps, 3_000);
  const overview = usePolled(() => api.metricsOverview(60), 3_000);
  const action = useAction();
  const { can } = useSession();
  const [creating, setCreating] = useState(false);

  const control = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      apps.refresh();
      overview.refresh();
    }
  };

  const host = overview.data?.host;
  const appHist = overview.data?.apps.history ?? {};
  const traffic = overview.data?.traffic ?? {};
  const runningCount = apps.data?.filter((a) => a.state === "RUNNING" || a.state === "STARTING").length ?? 0;

  return (
    <div className="page page-monitor">
      <div className="page-head">
        <div>
          <h1>모니터링</h1>
          <p className="muted small">
            {apps.data ? `${apps.data.length}개 앱 · ${runningCount}개 실행 중` : "불러오는 중…"} · 5초마다 갱신
          </p>
        </div>
        {can("admin") && (
          <button className="btn btn-primary" onClick={() => setCreating(true)}>
            앱 추가
          </button>
        )}
      </div>

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}
      {apps.error && <div className="alert">{apps.error}</div>}

      <HostOverview host={host} appCount={apps.data?.length ?? 0} running={runningCount} />

      <LiveTransactions traffic={traffic} apps={apps.data ?? []} />

      {creating && (
        <CreateAppForm
          onDone={() => {
            setCreating(false);
            apps.refresh();
          }}
          onCancel={() => setCreating(false)}
        />
      )}

      {apps.loading && !apps.data && <div className="muted">불러오는 중…</div>}

      {apps.data?.length === 0 && (
        <div className="empty">
          <p>등록된 앱이 없습니다.</p>
          <p className="muted">앱을 추가하고 jar를 업로드하면 이 PC에서 바로 기동할 수 있습니다.</p>
        </div>
      )}

      {apps.data && apps.data.length > 0 && (
        <div className="monitor-grid">
          {apps.data.map((app) => (
            <AppMonitorCard
              key={app.name}
              app={app}
              history={appHist[String(app.id)] ?? []}
              traffic={traffic[String(app.id)]}
              canOperate={can("operator")}
              busy={action.busy}
              onControl={control}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function HostOverview({
  host,
  appCount,
  running,
}: {
  host: { latest: HostReading; history: HostReading[] } | undefined;
  appCount: number;
  running: number;
}) {
  const h = host?.latest;
  const hist = host?.history ?? [];
  if (!h || !h.ts) {
    return (
      <section className="card">
        <h2>JAVIS Server</h2>
        <p className="muted small">호스트 샘플을 기다리는 중…</p>
      </section>
    );
  }
  const memPct = h.memTotal > 0 ? (h.memUsed / h.memTotal) * 100 : 0;
  const diskPct = h.diskTotal > 0 ? (1 - h.diskFree / h.diskTotal) * 100 : 0;

  return (
    <section className="card">
      <h2>JAVIS Server</h2>
      <div className="metric-grid metric-grid-4">
        <div>
          <div className="metric-label">CPU</div>
          <div className="metric-value">{h.cpuPercent.toFixed(1)}%</div>
          <Sparkline values={hist.map((s) => s.cpuPercent)} min={0} color="var(--accent)" />
        </div>
        <div>
          <div className="metric-label">메모리</div>
          <div className="metric-value">
            {formatBytes(h.memUsed)}
            <span className="muted small"> ({memPct.toFixed(0)}%)</span>
          </div>
          <Sparkline values={hist.map((s) => s.memUsed)} color="var(--ok)" />
        </div>
        <div>
          <div className="metric-label">디스크</div>
          <div className="metric-value">
            {formatBytes(h.diskFree)}
            <span className="muted small"> 남음 ({diskPct.toFixed(0)}%)</span>
          </div>
          <Sparkline values={hist.map((s) => s.diskFree)} color="var(--warn)" />
        </div>
        <div>
          <div className="metric-label">프로세스 / 앱</div>
          <div className="metric-value">
            {h.loadProcs}
            <span className="muted small">
              {" "}
              / {running}/{appCount}
            </span>
          </div>
          <Sparkline values={hist.map((s) => s.loadProcs)} color="#a371f7" />
        </div>
      </div>
    </section>
  );
}

function LiveTransactions({
  traffic,
  apps,
}: {
  traffic: Record<string, TrafficSnapshot>;
  apps: App[];
}) {
  const names = new Map(apps.map((a) => [a.id, a.displayName || a.name]));
  const all: (HTTPExchange & { app?: string })[] = [];
  for (const snap of Object.values(traffic)) {
    const label = names.get(snap.appId) || snap.appName;
    for (const e of snap.recent ?? []) {
      all.push({ ...e, app: label });
    }
  }
  all.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  const active = Object.values(traffic).reduce((n, s) => n + (s.active || 0), 0);
  const tps = Object.values(traffic).reduce((n, s) => n + (s.tps || 0), 0);

  return (
    <section className="card traffic-panel">
      <div className="page-head">
        <div>
          <h2>실시간 트랜잭션</h2>
          <p className="muted small">앱으로 들어온 HTTP 요청 · 3초마다 갱신</p>
        </div>
        <span className={active > 0 ? "traffic-live on" : "traffic-live"}>
          {active > 0 ? `${active}건 진행 중 · ${tps.toFixed(1)} TPS` : `${tps.toFixed(1)} TPS`}
        </span>
      </div>
      <XView exchanges={all} height={100} />
      <ExchangeTable exchanges={all} showApp />
    </section>
  );
}

function AppMonitorCard({
  app,
  history,
  traffic,
  canOperate,
  busy,
  onControl,
}: {
  app: App;
  history: ProcessReading[];
  traffic?: TrafficSnapshot;
  canOperate: boolean;
  busy: boolean;
  onControl: (fn: () => Promise<unknown>) => void;
}) {
  const running = app.state === "RUNNING" || app.state === "STARTING";
  return (
    <article className="card monitor-card">
      <div className="monitor-card-head">
        <div>
          <Link className="app-link" to={`/apps/${encodeURIComponent(app.name)}`}>
            {app.displayName || app.name}
          </Link>
          {app.displayName && <div className="muted small">{app.name}</div>}
        </div>
        <StateBadge state={app.state} pid={app.pid} />
      </div>

      <dl className="monitor-meta">
        <div>
          <dt>가동</dt>
          <dd>{running ? formatDuration(app.startedAt) : "—"}</dd>
        </div>
        <div>
          <dt>재시작</dt>
          <dd>{app.restartCount}</dd>
        </div>
        <div>
          <dt>실행 / 승격</dt>
          <dd className="mono small">
            {app.runningVersion || "—"} / {app.activeVersion || "미승격"}
            {running &&
              app.activeVersion &&
              app.runningVersion &&
              app.activeVersion !== app.runningVersion && (
                <span className="pill pill-warn">재시작 필요</span>
              )}
          </dd>
        </div>
      </dl>

      <div className="metric-grid metric-grid-4">
        <div>
          <div className="metric-label">CPU</div>
          <div className="metric-value">
            {running && app.cpuPercent !== undefined ? `${app.cpuPercent.toFixed(1)}%` : "—"}
          </div>
          <Sparkline values={history.map((s) => s.cpuPercent)} min={0} color="var(--accent)" />
        </div>
        <div>
          <div className="metric-label">RSS</div>
          <div className="metric-value">{running && app.rssBytes ? formatBytes(app.rssBytes) : "—"}</div>
          <Sparkline values={history.map((s) => s.rssBytes)} color="var(--ok)" />
        </div>
        <div>
          <div className="metric-label">TPS</div>
          <div className="metric-value">{running ? (traffic?.tps ?? 0).toFixed(1) : "—"}</div>
          <Sparkline values={(traffic?.history ?? []).map((s) => s.tps)} min={0} color="var(--accent)" />
        </div>
        <div>
          <div className="metric-label">진행 중 / 평균</div>
          <div className="metric-value">
            {running ? (traffic?.active ?? 0) : "—"}
            <span className="muted small"> / {running ? formatMs(traffic?.avgMs) : "—"}</span>
          </div>
        </div>
      </div>
      {running && traffic?.recent && traffic.recent.length > 0 && (
        <ul className="traffic-ticker">
          {[...traffic.recent]
            .reverse()
            .slice(0, 4)
            .map((e, i) => (
              <li key={`${e.at}-${e.path}-${i}`}>
                <span className="mono">{e.method}</span>
                <span className="traffic-path">{e.path}</span>
                <span className={`pill ${e.status >= 400 ? "pill-warn" : "pill-ok"}`}>{e.status || "—"}</span>
                <span className="muted small">{e.ms ? `${Math.round(e.ms)}ms` : ""}</span>
              </li>
            ))}
        </ul>
      )}

      <div className="monitor-card-actions">
        {!canOperate ? (
          <span className="muted small">읽기 전용</span>
        ) : running ? (
          <>
            <button className="btn btn-sm" disabled={busy} onClick={() => onControl(() => api.restart(app.name))}>
              재시작
            </button>
            <button
              className="btn btn-sm btn-danger"
              disabled={busy}
              onClick={() => onControl(() => api.stop(app.name))}
            >
              정지
            </button>
          </>
        ) : (
          <button
            className="btn btn-sm btn-primary"
            disabled={busy || app.artifactCount === 0}
            title={app.artifactCount === 0 ? "먼저 jar를 업로드하세요" : undefined}
            onClick={() => onControl(() => api.start(app.name))}
          >
            기동
          </button>
        )}
        <Link className="btn btn-sm" to={`/apps/${encodeURIComponent(app.name)}`}>
          상세
        </Link>
      </div>
    </article>
  );
}

function CreateAppForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [shutdownUrl, setShutdownUrl] = useState("");
  const action = useAction();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const ok = await action.run(() =>
      api.createApp({
        name: name.trim(),
        displayName: displayName.trim() || undefined,
        shutdownUrl: shutdownUrl.trim() || undefined,
      }),
    );
    if (ok) onDone();
  };

  return (
    <form className="card form" onSubmit={submit}>
      <h2>앱 추가</h2>

      <label>
        <span>서버 이름</span>
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="order-api" required autoFocus />
        <small className="muted">
          저장소 디렉터리와 API 경로에 쓰입니다. 영문·숫자·<code>.</code>
          <code>-</code>
          <code>_</code>만 가능합니다.
        </small>
      </label>

      <label>
        <span>표시 이름 (선택)</span>
        <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="주문 API" />
      </label>

      <label>
        <span>Actuator shutdown URL (선택)</span>
        <input
          value={shutdownUrl}
          onChange={(e) => setShutdownUrl(e.target.value)}
          placeholder="http://127.0.0.1:8080/actuator/shutdown"
        />
      </label>

      {action.error && <div className="alert">{action.error}</div>}

      <div className="form-actions">
        <button type="button" className="btn" onClick={onCancel}>
          취소
        </button>
        <button type="submit" className="btn btn-primary" disabled={action.busy}>
          {action.busy ? "만드는 중…" : "만들기"}
        </button>
      </div>
    </form>
  );
}
