import { api, formatBytes } from "../api";
import { usePolled } from "../hooks";
import { Sparkline } from "./Sparkline";

export function MetricsPanel({ appName }: { appName: string }) {
  const metrics = usePolled(() => api.appMetrics(appName, 60), 5_000);
  const latest = metrics.data?.latest;
  const history = metrics.data?.history ?? [];

  return (
    <section className="card">
      <h2>리소스</h2>
      <p className="muted small">최근 1시간. 5초마다 샘플링합니다.</p>

      {metrics.error && <div className="alert">{metrics.error}</div>}

      <div className="metric-grid">
        <div>
          <div className="metric-label">CPU</div>
          <div className="metric-value">{(latest?.cpuPercent ?? 0).toFixed(1)}%</div>
          <Sparkline values={history.map((s) => s.cpuPercent)} color="var(--accent)" />
        </div>
        <div>
          <div className="metric-label">RSS</div>
          <div className="metric-value">{formatBytes(latest?.rssBytes ?? 0)}</div>
          <Sparkline values={history.map((s) => s.rssBytes)} color="var(--ok)" />
        </div>
        <div>
          <div className="metric-label">스레드 / 핸들</div>
          <div className="metric-value">
            {latest?.threads ?? 0}
            <span className="muted small"> / {latest?.handles ?? 0}</span>
          </div>
          <Sparkline values={history.map((s) => s.threads)} color="var(--warn)" />
        </div>
      </div>
    </section>
  );
}

export function HostMetrics() {
  const now = usePolled(api.hostNow, 5_000);
  const hist = usePolled(() => api.hostMetrics(60), 15_000);
  const h = now.data;
  if (!h || h.ts === 0) {
    return null;
  }

  const memPct = h.memTotal > 0 ? (h.memUsed / h.memTotal) * 100 : 0;
  const diskPct = h.diskTotal > 0 ? (1 - h.diskFree / h.diskTotal) * 100 : 0;

  return (
    <section className="card">
      <h2>이 PC</h2>
      <div className="metric-grid">
        <div>
          <div className="metric-label">CPU</div>
          <div className="metric-value">{h.cpuPercent.toFixed(1)}%</div>
          <Sparkline values={(hist.data ?? []).map((s) => s.cpuPercent)} />
        </div>
        <div>
          <div className="metric-label">메모리</div>
          <div className="metric-value">
            {formatBytes(h.memUsed)}
            <span className="muted small"> / {formatBytes(h.memTotal)} ({memPct.toFixed(0)}%)</span>
          </div>
        </div>
        <div>
          <div className="metric-label">디스크 (데이터 루트)</div>
          <div className="metric-value">
            {formatBytes(h.diskFree)}
            <span className="muted small"> 남음 ({diskPct.toFixed(0)}% 사용)</span>
          </div>
        </div>
      </div>
    </section>
  );
}
