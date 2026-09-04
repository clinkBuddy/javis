import { useCallback, useEffect, useRef, useState } from "react";

export interface Polled<T> {
  data: T | undefined;
  error: string | undefined;
  loading: boolean;
  /** refresh re-fetches immediately and resets the interval, so an action can
   *  update the view without waiting out the poll delay. */
  refresh: () => void;
}

/**
 * usePolled fetches on mount and then on an interval.
 *
 * Process state changes outside the UI — an app crashes, someone stops it from
 * another browser — so a view that only loads once would quietly go stale.
 * Polling is used rather than websockets because the payloads are small and
 * the reconnect handling that a socket needs is not worth it here.
 */
export function usePolled<T>(fetcher: () => Promise<T>, intervalMs: number): Polled<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);

  // Held in a ref so that a fetcher defined inline in a component does not
  // restart the interval on every render.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const result = await fetcherRef.current();
        if (cancelled) return;
        setData(result);
        setError(undefined);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    void load();
    const timer = window.setInterval(load, intervalMs);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [intervalMs, tick]);

  const refresh = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, refresh };
}

export interface Action {
  run: (fn: () => Promise<unknown>) => Promise<boolean>;
  busy: boolean;
  error: string | undefined;
  clearError: () => void;
}

/**
 * useAction wraps a mutating call with busy and error state.
 *
 * Start, stop and restart take seconds, and letting the button be clicked
 * again in that window is how an operator ends up with two start requests for
 * one app.
 */
export function useAction(): Action {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);

  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return false;
    } finally {
      setBusy(false);
    }
  }, []);

  return { run, busy, error, clearError: useCallback(() => setError(undefined), []) };
}
