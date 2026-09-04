import { useEffect, useRef, useState } from "react";

import { api } from "../api";

export function LogPanel({ appName }: { appName: string }) {
  const [lines, setLines] = useState<string[]>([]);
  const [path, setPath] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [paused, setPaused] = useState(false);
  const scroller = useRef<HTMLPreElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  useEffect(() => {
    let cancelled = false;
    let source: EventSource | undefined;

    const start = async () => {
      try {
        const tail = await api.appLogs(appName, 250);
        if (cancelled) return;
        setPath(tail.path);
        setLines(tail.lines);
        setError(undefined);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
        return;
      }

      // EventSource is GET-only and sends cookies, which is exactly what the
      // stream endpoint expects. fetch + ReadableStream would also work but
      // reconnects would have to be written by hand.
      source = new EventSource(`/api/v1/apps/${encodeURIComponent(appName)}/logs/stream`);
      source.addEventListener("chunk", (ev) => {
        if (pausedRef.current) return;
        try {
          const body = JSON.parse((ev as MessageEvent).data) as { text?: string };
          const incoming = (body.text ?? "").replace(/\r\n/g, "\n").split("\n");
          if (incoming[incoming.length - 1] === "") incoming.pop();
          if (incoming.length === 0) return;
          setLines((prev) => {
            const next = prev.concat(incoming);
            return next.length > 800 ? next.slice(next.length - 800) : next;
          });
        } catch {
          // A malformed event is skipped rather than tearing down the stream.
        }
      });
      source.addEventListener("error", () => {
        if (!cancelled) setError("로그 스트림이 끊겼습니다. 새로고침하면 다시 붙습니다.");
      });
    };

    void start();
    return () => {
      cancelled = true;
      source?.close();
    };
  }, [appName]);

  useEffect(() => {
    if (paused) return;
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines, paused]);

  return (
    <section className="card">
      <div className="page-head">
        <div>
          <h2>콘솔 로그</h2>
          {path && <p className="muted small mono">{path}</p>}
        </div>
        <button className="btn btn-sm" onClick={() => setPaused((p) => !p)}>
          {paused ? "따라가기" : "일시정지"}
        </button>
      </div>

      {error && <div className="alert">{error}</div>}

      <pre className="log-view" ref={scroller}>
        {lines.length === 0 ? <span className="muted">아직 출력된 로그가 없습니다.</span> : lines.join("\n")}
      </pre>
    </section>
  );
}
