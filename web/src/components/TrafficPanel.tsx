import type { HTTPExchange, TrafficSnapshot } from "../api";
import { Sparkline } from "./Sparkline";
import { XView } from "./XView";

export function TrafficPanel({ traffic }: { traffic?: TrafficSnapshot }) {
  const t = traffic;
  const recent = t?.recent ?? [];
  const hist = t?.history ?? [];
  const active = t?.active ?? 0;

  return (
    <section className="card traffic-panel">
      <div className="page-head">
        <div>
          <h2>실시간 트랜잭션</h2>
          <p className="muted small">
            {sourceLabel(t?.source)}
            {t?.listen && t.listen.length > 0 ? ` · listen ${t.listen.join(", ")}` : ""}
          </p>
        </div>
        <span className={active > 0 ? "traffic-live on" : "traffic-live"}>
          {active > 0 ? `${active}건 진행 중` : "대기"}
        </span>
      </div>

      <div className="metric-grid metric-grid-4">
        <div>
          <div className="metric-label">TPS</div>
          <div className="metric-value">{(t?.tps ?? 0).toFixed(1)}</div>
          <Sparkline values={hist.map((s) => s.tps)} min={0} color="var(--accent)" />
        </div>
        <div>
          <div className="metric-label">평균 응답</div>
          <div className="metric-value">{formatMs(t?.avgMs)}</div>
          <Sparkline values={hist.map((s) => s.avgMs)} min={0} color="var(--ok)" />
        </div>
        <div>
          <div className="metric-label">최대 응답</div>
          <div className="metric-value">{formatMs(t?.maxMs)}</div>
        </div>
        <div>
          <div className="metric-label">에러율</div>
          <div className="metric-value">{((t?.errorRate ?? 0) * 100).toFixed(1)}%</div>
        </div>
      </div>

      <h3>X-View</h3>
      <XView exchanges={recent} />

      <h3>최근 요청</h3>
      <ExchangeTable exchanges={recent} />
    </section>
  );
}

export function ExchangeTable({
  exchanges,
  showApp,
}: {
  exchanges: (HTTPExchange & { app?: string })[];
  showApp?: boolean;
}) {
  if (exchanges.length === 0) {
    return <p className="muted small">수집된 요청이 없습니다. Actuator httpexchanges가 열려 있거나 액세스 로그가 있으면 여기에 표시됩니다.</p>;
  }
  const rows = [...exchanges].reverse().slice(0, 40);
  return (
    <table className="table traffic-table">
      <thead>
        <tr>
          <th>시각</th>
          {showApp && <th>앱</th>}
          <th>메서드</th>
          <th>경로</th>
          <th>상태</th>
          <th className="right">응답</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((e, i) => (
          <tr key={`${e.at}-${e.path}-${i}`}>
            <td className="small nowrap">{formatClock(e.at)}</td>
            {showApp && <td className="small">{e.app}</td>}
            <td className="mono small">{e.method}</td>
            <td className="mono small traffic-path">{e.path}</td>
            <td>
              <span className={`pill ${statusPill(e.status)}`}>{e.status || "—"}</span>
            </td>
            <td className="right mono small">{e.ms ? `${Math.round(e.ms)}ms` : "—"}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function formatMs(ms?: number) {
  if (!ms) return "—";
  if (ms < 10) return `${ms.toFixed(1)}ms`;
  return `${Math.round(ms)}ms`;
}

function sourceLabel(src?: string) {
  if (src === "actuator") return "Spring Actuator에서 수집";
  if (src === "log") return "콘솔 로그에서 수집";
  if (src === "tcp") return "TCP 연결 기준 (HTTP 본문은 로그·Actuator 필요)";
  return "요청을 기다리는 중";
}

function statusPill(status: number) {
  if (status >= 500) return "pill-warn";
  if (status >= 400) return "pill-warn";
  if (status > 0) return "pill-ok";
  return "";
}

export function formatClock(iso: string) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString("ko-KR", { hour12: false });
}
