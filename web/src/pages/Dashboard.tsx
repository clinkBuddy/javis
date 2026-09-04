import { useState } from "react";
import { Link } from "react-router-dom";

import { api, formatBytes } from "../api";
import { useAction, usePolled } from "../hooks";
import { useSession } from "../session";
import { StateBadge } from "../components/StateBadge";
import { HostMetrics } from "../components/MetricsPanel";

export function Dashboard() {
  // Three seconds is short enough that a start or stop looks immediate and
  // long enough that the process scan it triggers on the server stays cheap.
  const apps = usePolled(api.listApps, 3_000);
  const action = useAction();
  const { can } = useSession();
  const [creating, setCreating] = useState(false);

  const control = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      apps.refresh();
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <h1>애플리케이션</h1>
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

      <HostMetrics />

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
          <p className="muted">
            앱을 추가하고 jar를 업로드하면 이 PC에서 바로 기동할 수 있습니다.
          </p>
        </div>
      )}

      {apps.data && apps.data.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>이름</th>
              <th>상태</th>
              <th>CPU</th>
              <th>RSS</th>
              <th>실행 버전</th>
              <th>승격 버전</th>
              <th className="right">제어</th>
            </tr>
          </thead>
          <tbody>
            {apps.data.map((app) => {
              const running = app.state === "RUNNING" || app.state === "STARTING";
              return (
                <tr key={app.name}>
                  <td>
                    <Link className="app-link" to={`/apps/${encodeURIComponent(app.name)}`}>
                      {app.displayName || app.name}
                    </Link>
                    {app.displayName && <div className="muted small">{app.name}</div>}
                  </td>
                  <td>
                    <StateBadge state={app.state} pid={app.pid} />
                  </td>
                  <td className="mono small">
                    {running && app.cpuPercent !== undefined ? `${app.cpuPercent.toFixed(1)}%` : "—"}
                  </td>
                  <td className="mono small">
                    {running && app.rssBytes ? formatBytes(app.rssBytes) : "—"}
                  </td>
                  <td className="mono">{app.runningVersion || "—"}</td>
                  <td className="mono">
                    {app.activeVersion || <span className="muted">미승격</span>}
                    {/* A promoted version that differs from the running one is
                        the single most common cause of "my fix isn't live". */}
                    {running &&
                      app.activeVersion &&
                      app.runningVersion &&
                      app.activeVersion !== app.runningVersion && (
                        <span className="pill pill-warn">재시작 필요</span>
                      )}
                  </td>
                  <td className="right nowrap">
                    {!can("operator") ? (
                      <span className="muted small">읽기 전용</span>
                    ) : running ? (
                      <>
                        <button
                          className="btn btn-sm"
                          disabled={action.busy}
                          onClick={() => control(() => api.restart(app.name))}
                        >
                          재시작
                        </button>
                        <button
                          className="btn btn-sm btn-danger"
                          disabled={action.busy}
                          onClick={() => control(() => api.stop(app.name))}
                        >
                          정지
                        </button>
                      </>
                    ) : (
                      <button
                        className="btn btn-sm btn-primary"
                        disabled={action.busy || app.artifactCount === 0}
                        title={app.artifactCount === 0 ? "먼저 jar를 업로드하세요" : undefined}
                        onClick={() => control(() => api.start(app.name))}
                      >
                        기동
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
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
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="order-api"
          required
          autoFocus
        />
        <small className="muted">
          저장소 디렉터리와 API 경로에 쓰입니다. 영문·숫자·<code>.</code>
          <code>-</code>
          <code>_</code>만 가능합니다.
        </small>
      </label>

      <label>
        <span>표시 이름 (선택)</span>
        <input
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          placeholder="주문 API"
        />
      </label>

      <label>
        <span>Actuator shutdown URL (선택)</span>
        <input
          value={shutdownUrl}
          onChange={(e) => setShutdownUrl(e.target.value)}
          placeholder="http://127.0.0.1:8080/actuator/shutdown"
        />
        <small className="muted">
          설정하면 정지할 때 이 엔드포인트를 먼저 호출하고, 실패하면 CTRL+C로
          넘어갑니다.
        </small>
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
