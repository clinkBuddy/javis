interface Props {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
}

/** Tiny SVG chart. A charting library would dwarf the rest of the UI for
 *  a few dozen points sampled every five seconds. */
export function Sparkline({ values, width = 280, height = 56, color = "var(--accent)" }: Props) {
  if (values.length < 2) {
    return <div className="sparkline-empty muted small">아직 샘플이 충분하지 않습니다.</div>;
  }

  const max = Math.max(...values, 0.001);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const step = (width - 2) / (values.length - 1);
  const points = values
    .map((v, i) => {
      const x = 1 + i * step;
      const y = height - 1 - ((v - min) / span) * (height - 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");

  return (
    <svg className="sparkline" width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      <polyline fill="none" stroke={color} strokeWidth="1.6" points={points} />
    </svg>
  );
}
