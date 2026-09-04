import { useState } from "react";

import { ApiError } from "../api";
import { useSession } from "../session";

export function Login() {
  const { signIn } = useSession();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [throttled, setThrottled] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    setThrottled(false);
    try {
      await signIn(username, password);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setThrottled(err instanceof ApiError && err.status === 429);
      setPassword("");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-screen">
      <form className="login-card" onSubmit={submit}>
        <div className="login-brand">
          <span className="brand-mark">J</span>
          <span className="brand-name">JARVIS</span>
        </div>

        <label>
          <span>사용자</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
          />
        </label>

        <label>
          <span>비밀번호</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        {error && <div className="alert">{error}</div>}

        <button className="btn btn-primary btn-block" type="submit" disabled={busy || throttled}>
          {busy ? "확인 중…" : "로그인"}
        </button>

        <p className="muted small login-hint">
          최초 계정은 <code>admin</code> / <code>admin</code> 입니다.
        </p>
      </form>
    </div>
  );
}
