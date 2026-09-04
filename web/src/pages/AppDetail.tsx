import { Link, useParams } from "react-router-dom";

import { api } from "../api";
import { useAction, usePolled } from "../hooks";
import { useSession } from "../session";
import { StateBadge } from "../components/StateBadge";
import { ArtifactPanel } from "../components/ArtifactPanel";
import { ProfileForm } from "../components/ProfileForm";
import { MetricsPanel } from "../components/MetricsPanel";
import { TrafficPanel } from "../components/TrafficPanel";
import { LogPanel } from "../components/LogPanel";
import { AppSettings } from "../components/AppSettings";
import { formatBytes } from "../api";

export function AppDetail() {
  const { name = "" } = useParams();
  const status = usePolled(() => api.status(name), 3_000);
  const app = usePolled(() => api.getApp(name), 10_000);
  const traffic = usePolled(() => api.appTraffic(name), 3_000);
  const action = useAction();
  const { can } = useSession();

  const control = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      status.refresh();
      app.refresh();
    }
  };

  const state = status.data?.state ?? app.data?.state ?? "STOPPED";
  const running = state === "RUNNING" || state === "STARTING";

  return (
    <div className="page">
      <div className="breadcrumb">
        <Link to="/">모니터</Link>
        <span>/</span>
        <span>{name}</span>
      </div>

      <div className="page-head">
        <div>
          <h1>{app.data?.displayName || name}</h1>
          <div className="head-status">
            <StateBadge state={state} pid={status.data?.pid} />
            {status.data?.runningVersion && (
              <span className="muted small">
                버전 <code>{status.data.runningVersion}</code>
                {status.data.runningRevision && (
                  <>
                    {" · "}프로파일 rev {status.data.runningRevision}
                  </>
                )}
              </span>
            )}
            {running && status.data?.cpuPercent !== undefined && (
              <span className="muted small">
                CPU {status.data.cpuPercent.toFixed(1)}%
                {status.data.rssBytes ? ` · RSS ${formatBytes(status.data.rssBytes)}` : ""}
                {status.data.restartCount ? ` · 재시작 ${status.data.restartCount}회` : ""}
              </span>
            )}
          </div>
        </div>

        {can("operator") && (
          <div className="nowrap">
            {running ? (
              <>
                <button
                  className="btn"
                  disabled={action.busy}
                  onClick={() => control(() => api.restart(name))}
                >
                  재시작
                </button>
                <button
                  className="btn btn-danger"
                  disabled={action.busy}
                  onClick={() => control(() => api.stop(name))}
                >
                  정지
                </button>
              </>
            ) : (
              <button
                className="btn btn-primary"
                disabled={action.busy}
                onClick={() => control(() => api.start(name))}
              >
                기동
              </button>
            )}
          </div>
        )}
      </div>

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}

      {status.data?.commandLine && running && (
        <section className="card">
          <h2>실행 중인 명령</h2>
          <pre className="code">{status.data.commandLine}</pre>
          {status.data.consoleLog && (
            <p className="muted small">
              콘솔 로그: <code>{status.data.consoleLog}</code>
            </p>
          )}
        </section>
      )}

      {running && <TrafficPanel traffic={traffic.data} />}
      {running && <MetricsPanel appName={name} />}
      <LogPanel appName={name} />

      <ArtifactPanel
        appName={name}
        running={running}
        runningVersion={status.data?.runningVersion}
        onChanged={() => {
          app.refresh();
          status.refresh();
        }}
      />

      <ProfileForm appName={name} running={running} />
      {app.data && <AppSettings app={app.data} onSaved={() => app.refresh()} />}
    </div>
  );
}
