import type { HTTPExchange } from "../api";

/** Jennifer-style scatter: time on X, response ms on Y. */
export function XView({
  exchanges,
  height = 88,
}: {
  exchanges: HTTPExchange[];
  height?: number;
}) {
  const pts = exchanges.filter((e) => e.ms > 0 || e.status > 0);
  if (pts.length === 0) {
    return <div className="xview-empty muted small">요청이 아직 없습니다</div>;
  }

  const times = pts.map((e) => Date.parse(e.at) || 0);
  const minT = Math.min(...times);
  const maxT = Math.max(...times, minT + 1);
  const maxMs = Math.max(50, ...pts.map((e) => e.ms || 0));
  const w = 320;
  const pad = 6;

  return (
    <svg className="xview" viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none">
      {pts.map((e, i) => {
        const t = Date.parse(e.at) || minT;
        const x = pad + ((t - minT) / (maxT - minT)) * (w - pad * 2);
        const y = height - pad - ((e.ms || 0) / maxMs) * (height - pad * 2);
        return (
          <circle
            key={`${e.at}-${e.path}-${i}`}
            cx={x}
            cy={Math.max(pad, y)}
            r="2.4"
            fill={statusColor(e.status)}
          >
            <title>
              {e.method} {e.path} {e.status || ""} {e.ms ? `${Math.round(e.ms)}ms` : ""}
            </title>
          </circle>
        );
      })}
    </svg>
  );
}

function statusColor(status: number): string {
  if (status >= 500) return "var(--danger)";
  if (status >= 400) return "var(--warn)";
  if (status > 0) return "var(--ok)";
  return "var(--accent)";
}
