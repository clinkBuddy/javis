import { useState } from "react";

import { api } from "../api";
import { useSession } from "../session";

const MIN_LENGTH = 12;

/**
 * ChangePassword is both the forced-change gate and the account page.
 *
 * While must_change is set the server refuses every other endpoint, so this
 * is rendered instead of the app.
 */
export function ChangePassword({ forced }: { forced: boolean }) {
  const { user, refresh, signOut } = useSession();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [username, setUsername] = useState(user?.username ?? "");
  const [error, setError] = useState<string | undefined>();
  const [done, setDone] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  const submitPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (next !== confirm) {
      setError("새 비밀번호와 확인이 일치하지 않습니다.");
      return;
    }
    if (next.length < MIN_LENGTH) {
      setError(`새 비밀번호는 ${MIN_LENGTH}자 이상이어야 합니다.`);
      return;
    }
    if (next === "admin") {
      setError("최초 비밀번호는 다시 사용할 수 없습니다.");
      return;
    }

    setBusy(true);
    setError(undefined);
    try {
      await api.changePassword(current, next);
      setCurrent("");
      setNext("");
      setConfirm("");
      setDone("비밀번호를 변경했습니다. 다른 브라우저의 세션은 모두 로그아웃되었습니다.");
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const submitUsername = async (e: React.FormEvent) => {
    e.preventDefault();
    const nextName = username.trim().toLowerCase();
    if (!nextName || nextName === user?.username) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await api.changeUsername(nextName);
      setDone(`아이디를 ${nextName}(으)로 변경했습니다.`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const passwordForm = (
    <form className="card form" onSubmit={submitPassword}>
      <h2>{forced ? "비밀번호를 변경하세요" : "비밀번호 변경"}</h2>
      {forced && (
        <p className="muted small">
          최초(또는 관리자가 지정한) 비밀번호로는 서비스를 이용할 수 없습니다.
          12자 이상의 새 비밀번호를 설정한 뒤에 대시보드로 이동합니다.
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
      {done && !forced && <div className="notice">{done}</div>}

      <div className="form-actions">
        {forced && (
          <button type="button" className="btn" onClick={() => void signOut()}>
            로그아웃
          </button>
        )}
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "변경 중…" : "비밀번호 변경"}
        </button>
      </div>
    </form>
  );

  const usernameForm = (
    <form className="card form" onSubmit={submitUsername}>
      <h2>아이디 변경</h2>
      <p className="muted small">영문 소문자, 숫자, 점, 대시, 밑줄. 2–32자.</p>
      <label className="narrow">
        <span>아이디</span>
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value.toLowerCase())}
          autoComplete="username"
          required
        />
      </label>
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={busy}>
          {busy ? "변경 중…" : "아이디 변경"}
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
        {usernameForm}
        {passwordForm}
      </div>
    );
  }

  return (
    <div className="login-screen login-screen-stack">
      {passwordForm}
      {usernameForm}
    </div>
  );
}
