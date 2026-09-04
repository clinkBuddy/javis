interface Props {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
  /** When set, the chart uses a fixed range instead of the sample min/max. */
  min?: number;
  max?: number;
}

/** Tiny SVG chart. A charting library would dwarf the rest of the UI for
 *  a few dozen points sampled every five seconds. */
export function Sparkline({
  values,
  width = 280,
  height = 56,
  color = "var(--accent)",
  min,
  max,
}: Props) {
  if (values.length < 2) {
    return <div className="sparkline-empty muted small">샘플 수집 중…</div>;
  }

  const lo = min ?? Math.min(...values, 0);
  const hi = max ?? Math.max(...values, lo + 0.001);
  const span = hi - lo || 1;
  const step = (width - 2) / (values.length - 1);
  const pts = values.map((v, i) => {
    const x = 1 + i * step;
    const y = height - 1 - ((v - lo) / span) * (height - 2);
    return { x, y };
  });
  const line = pts.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
  const area =
    `1,${height - 1} ` +
    line +
    ` ${pts[pts.length - 1].x.toFixed(1)},${height - 1}`;

  return (
    <svg className="sparkline" width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      <polyline fill={color} fillOpacity="0.18" stroke="none" points={area} />
      <polyline fill="none" stroke={color} strokeWidth="1.6" points={line} />
    </svg>
  );
}
