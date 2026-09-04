import type { AppState } from "../api";

// Wording is in Korean to match the rest of the UI; the class name keeps the
// machine state so the colour mapping stays obvious.
const LABELS: Record<AppState, string> = {
  RUNNING: "실행 중",
  STARTING: "기동 중",
  STOPPING: "정지 중",
  STOPPED: "정지",
  FAILED: "실패",
  ORPHAN: "고아 프로세스",
};

export function StateBadge({ state, pid }: { state: AppState; pid?: number }) {
  const label = LABELS[state] ?? state;
  const showPid = pid !== undefined && pid > 0 && (state === "RUNNING" || state === "STARTING");

  return (
    <span className={`badge badge-${state.toLowerCase()}`}>
      {label}
      {showPid && <span className="badge-pid">PID {pid}</span>}
    </span>
  );
}
