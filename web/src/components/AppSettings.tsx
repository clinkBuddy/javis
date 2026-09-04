import { useEffect, useState } from "react";

import { api, type App } from "../api";
import { useAction } from "../hooks";
import { useSession } from "../session";

export function AppSettings({ app, onSaved }: { app: App; onSaved: () => void }) {
  const { can } = useSession();
  const readOnly = !can("admin");
  const action = useAction();
  const [saved, setSaved] = useState<string | undefined>();

  const [displayName, setDisplayName] = useState(app.displayName);
  const [shutdownUrl, setShutdownUrl] = useState(app.shutdownUrl ?? "");
  const [autostart, setAutostart] = useState(app.autostart);
  const [watchdog, setWatchdog] = useState(app.watchdog);
  const [maxRestarts, setMaxRestarts] = useState(String(app.maxRestarts));
  const [startOrder, setStartOrder] = useState(String(app.startOrder));
  const [startDelaySec, setStartDelaySec] = useState(String(app.startDelaySec));
  const [stopTimeoutSec, setStopTimeoutSec] = useState(String(app.stopTimeoutSec));
  const [dependsOn, setDependsOn] = useState((app.dependsOn ?? []).join("\n"));

  useEffect(() => {
    setDisplayName(app.displayName);
    setShutdownUrl(app.shutdownUrl ?? "");
    setAutostart(app.autostart);
    setWatchdog(app.watchdog);
    setMaxRestarts(String(app.maxRestarts));
    setStartOrder(String(app.startOrder));
    setStartDelaySec(String(app.startDelaySec));
    setStopTimeoutSec(String(app.stopTimeoutSec));
    setDependsOn((app.dependsOn ?? []).join("\n"));
  }, [app]);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const ok = await action.run(() =>
      api.updateApp(app.name, {
        displayName: displayName.trim(),
        description: app.description,
        shutdownUrl: shutdownUrl.trim(),
        autostart,
        watchdog,
        maxRestarts: Number(maxRestarts) || 0,
        startOrder: Number(startOrder) || 100,
        startDelaySec: Number(startDelaySec) || 0,
        stopTimeoutSec: Number(stopTimeoutSec) || 30,
        dependsOn: dependsOn
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean),
      }),
    );
    if (ok) {
      setSaved("저장했습니다. 워치독과 autostart는 다음 기동부터 적용됩니다.");
      onSaved();
    }
  };

  return (
    <section className="card">
      <h2>앱 설정</h2>
      <form className="form" onSubmit={save}>
        <div className="grid-2">
          <label>
            <span>표시 이름</span>
            <input value={displayName} disabled={readOnly} onChange={(e) => setDisplayName(e.target.value)} />
          </label>
          <label>
            <span>Actuator shutdown URL</span>
            <input
              value={shutdownUrl}
              disabled={readOnly}
              onChange={(e) => setShutdownUrl(e.target.value)}
              placeholder="http://127.0.0.1:8080/actuator/shutdown"
            />
          </label>
        </div>

        <div className="grid-3">
          <label className="check">
            <input
              type="checkbox"
              checked={autostart}
              disabled={readOnly}
              onChange={(e) => setAutostart(e.target.checked)}
            />
            JARVIS 시작 시 자동 기동
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={watchdog}
              disabled={readOnly}
              onChange={(e) => setWatchdog(e.target.checked)}
            />
            크래시 시 자동 재시작
          </label>
          <label>
            <span>최대 재시작 횟수 (0 = 무제한)</span>
            <input
              value={maxRestarts}
              disabled={readOnly}
              onChange={(e) => setMaxRestarts(e.target.value)}
            />
          </label>
        </div>

        <div className="grid-3">
          <label>
            <span>기동 순서</span>
            <input value={startOrder} disabled={readOnly} onChange={(e) => setStartOrder(e.target.value)} />
          </label>
          <label>
            <span>기동 지연 (초)</span>
            <input value={startDelaySec} disabled={readOnly} onChange={(e) => setStartDelaySec(e.target.value)} />
          </label>
          <label>
            <span>정지 대기 (초)</span>
            <input value={stopTimeoutSec} disabled={readOnly} onChange={(e) => setStopTimeoutSec(e.target.value)} />
          </label>
        </div>

        <label>
          <span>의존 앱 (한 줄에 하나, 이 앱보다 먼저 떠 있어야 함)</span>
          <textarea
            rows={3}
            value={dependsOn}
            disabled={readOnly}
            onChange={(e) => setDependsOn(e.target.value)}
            placeholder={"auth-api\nconfig-server"}
            spellCheck={false}
          />
        </label>

        {action.error && (
          <div className="alert" onClick={action.clearError}>
            {action.error}
          </div>
        )}
        {saved && <div className="notice">{saved}</div>}

        {!readOnly && (
          <div className="form-actions">
            <button type="submit" className="btn btn-primary" disabled={action.busy}>
              {action.busy ? "저장 중…" : "설정 저장"}
            </button>
          </div>
        )}
      </form>
    </section>
  );
}
