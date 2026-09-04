import { useState } from "react";

import { api } from "../api";
import { useSession } from "../session";

// Kept in step with auth.MinPasswordLength. The server rejects anything
// shorter; checking here just avoids a round trip to learn that.
const MIN_LENGTH = 12;

/**
 * ChangePassword doubles as the forced-change screen.
 *
 * `forced` is true when the account still carries must_change — after an
 * admin reset. In that state the server refuses every other endpoint, so this
 * is rendered instead of the app rather than alongside it.
 */
export function ChangePassword({ forced }: { forced: boolean }) {
  const { refresh, signOut } = useSession();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setError("새 비밀번호와 확인이 일치하지 않습니다.");
      return;
    }
    if (next.length < MIN_LENGTH) {
      setError(`새 비밀번호는 ${MIN_LENGTH}자 이상이어야 합니다.`);
      return;
    }

    setBusy(true);
    setError(undefined);
    try {
      await api.changePassword(current, next);
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone(true);
      // The server cleared must_change, so re-reading the session is what
      // unlocks the rest of the UI.
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const form = (
    <form className="card form" onSubmit={submit}>
      <h2>비밀번호 변경</h2>
      {forced && (
        <p className="muted small">
          지금 사용 중인 비밀번호는 자동 생성되었거나 관리자가 지정한 것입니다.
          계속하려면 새 비밀번호를 설정하세요.
        </p>
      )}

      <label>
        <span>현재 비밀번호</span>
        <input
          type="password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          autoComplete="current-password"
          required
          autoFocus
        />
      </label>

      <label>
        <span>새 비밀번호</span>
        <input
          type="password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          autoComplete="new-password"
          minLength={MIN_LENGTH}
          required
        />
        <small className="muted">
          {MIN_LENGTH}자 이상. 길이가 길수록 안전하며 문자 종류 제한은 없습니다.
        </small>
      </label>

      <label>
        <span>새 비밀번호 확인</span>
        <input
          type="password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="new-password"
          required
        />
      </label>

      {error && <div className="alert">{error}</div>}
      {done && !forced && (
        <div className="notice">
          변경했습니다. 다른 브라우저의 세션은 모두 로그아웃되었습니다.
        </div>
      )}

      <div className="form-actions">
        {forced && (
          <button type="button" className="btn" onClick={() => void signOut()}>
            로그아웃
          </button>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "변경 중…" : "변경"}
        </button>
      </div>
    </form>
  );

  if (!forced) {
    return (
      <div className="page">
        <div className="page-head">
          <h1>내 계정</h1>
        </div>
        {form}
      </div>
    );
  }

  return <div className="login-screen">{form}</div>;
}
