import { useState } from "react";

import { api, type Role, type User } from "../api";
import { useAction, usePolled } from "../hooks";
import { useSession } from "../session";

const ROLES: Role[] = ["admin", "operator", "viewer"];

const ROLE_LABELS: Record<Role, string> = {
  admin: "관리자",
  operator: "운영자",
  viewer: "조회자",
};

export function Users() {
  const users = usePolled(api.listUsers, 30_000);
  const audit = usePolled(() => api.listAudit(100), 15_000);
  const action = useAction();
  const { user: me } = useSession();
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<User | undefined>();
  const [renaming, setRenaming] = useState<User | undefined>();

  const act = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      users.refresh();
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <h1>사용자</h1>
        <button className="btn btn-primary" onClick={() => setCreating(true)}>
          사용자 추가
        </button>
      </div>

      <p className="muted small">
        관리자는 앱·JDK·계정을 만들고 지울 수 있고, 운영자는 기동·정지·업로드와
        JVM 옵션 변경까지, 조회자는 읽기만 가능합니다.
      </p>

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}
      {users.error && <div className="alert">{users.error}</div>}

      {creating && (
        <CreateUserForm
          onDone={() => {
            setCreating(false);
            users.refresh();
          }}
          onCancel={() => setCreating(false)}
        />
      )}

      {resetting && (
        <ResetPasswordForm
          target={resetting}
          onDone={() => {
            setResetting(undefined);
            users.refresh();
          }}
          onCancel={() => setResetting(undefined)}
        />
      )}

      {renaming && (
        <RenameUserForm
          target={renaming}
          onDone={() => {
            setRenaming(undefined);
            users.refresh();
          }}
          onCancel={() => setRenaming(undefined)}
        />
      )}

      {users.data && (
        <table className="table">
          <thead>
            <tr>
              <th>사용자</th>
              <th>역할</th>
              <th>상태</th>
              <th>마지막 로그인</th>
              <th className="right" />
            </tr>
          </thead>
          <tbody>
            {users.data.map((u) => {
              const isMe = u.id === me?.id;
              return (
                <tr key={u.id}>
                  <td>
                    {u.username}
                    {isMe && <span className="pill">나</span>}
                  </td>
                  <td>
                    <select
                      value={u.role}
                      disabled={action.busy || isMe}
                      title={isMe ? "자신의 역할은 변경할 수 없습니다" : undefined}
                      onChange={(e) =>
                        act(() => api.updateUser(u.id, e.target.value as Role, u.disabled))
                      }
                    >
                      {ROLES.map((r) => (
                        <option key={r} value={r}>
                          {ROLE_LABELS[r]}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {u.disabled ? (
                      <span className="badge badge-failed">사용 중지</span>
                    ) : u.mustChange ? (
                      <span className="badge badge-starting">변경 대기</span>
                    ) : (
                      <span className="badge badge-running">활성</span>
                    )}
                  </td>
                  <td className="small nowrap">{u.lastLoginAt || "—"}</td>
                  <td className="right nowrap">
                    <button
                      className="btn btn-sm"
                      disabled={action.busy}
                      onClick={() => setRenaming(u)}
                    >
                      아이디 변경
                    </button>
                    <button
                      className="btn btn-sm"
                      disabled={action.busy}
                      onClick={() => setResetting(u)}
                    >
                      비밀번호 재설정
                    </button>
                    {!isMe && (
                      <>
                        <button
                          className="btn btn-sm"
                          disabled={action.busy}
                          onClick={() => act(() => api.updateUser(u.id, u.role, !u.disabled))}
                        >
                          {u.disabled ? "사용" : "중지"}
                        </button>
                        <button
                          className="btn btn-sm btn-danger"
                          disabled={action.busy}
                          onClick={() => act(() => api.deleteUser(u.id))}
                        >
                          삭제
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      <section className="card">
        <h2>감사 로그</h2>
        <p className="muted small">
          조회를 제외한 모든 변경 요청이 기록됩니다. 이 표는 지워지지 않습니다.
        </p>

        {audit.data && audit.data.length > 0 ? (
          <table className="table">
            <thead>
              <tr>
                <th>시각</th>
                <th>사용자</th>
                <th>출처</th>
                <th>동작</th>
                <th>대상</th>
                <th>결과</th>
              </tr>
            </thead>
            <tbody>
              {audit.data.map((e) => (
                <tr key={e.id}>
                  <td className="small nowrap">{e.at}</td>
                  <td className="small">{e.username || "—"}</td>
                  <td className="mono small">{e.remoteAddr}</td>
                  <td className="mono small">{e.action}</td>
                  <td className="mono small">{e.target}</td>
                  <td className="small">
                    {e.result === "ok" ? (
                      <span className="pill pill-ok">성공</span>
                    ) : (
                      <span className="pill pill-warn">{e.detail}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="muted small">기록이 없습니다.</p>
        )}
      </section>
    </div>
  );
}

function CreateUserForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const action = useAction();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await action.run(() => api.createUser(username.trim(), password, role))) {
      onDone();
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <h2>사용자 추가</h2>

      <div className="grid-3">
        <label>
          <span>사용자 이름</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value.toLowerCase())}
            placeholder="ops"
            autoFocus
            required
          />
          <small className="muted">영문 소문자·숫자·. - _</small>
        </label>

        <label>
          <span>초기 비밀번호</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={12}
            autoComplete="new-password"
            required
          />
          <small className="muted">첫 로그인 때 변경하도록 강제됩니다.</small>
        </label>

        <label>
          <span>역할</span>
          <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {ROLE_LABELS[r]}
              </option>
            ))}
          </select>
        </label>
      </div>

      {action.error && <div className="alert">{action.error}</div>}

      <div className="form-actions">
        <button type="button" className="btn" onClick={onCancel}>
          취소
        </button>
        <button type="submit" className="btn btn-primary" disabled={action.busy}>
          {action.busy ? "만드는 중…" : "만들기"}
        </button>
      </div>
    </form>
  );
}

function ResetPasswordForm({
  target,
  onDone,
  onCancel,
}: {
  target: User;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [password, setPassword] = useState("");
  const action = useAction();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await action.run(() => api.resetUserPassword(target.id, password))) {
      onDone();
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <h2>
        <code>{target.username}</code> 비밀번호 재설정
      </h2>
      <p className="muted small">
        이 계정의 모든 세션이 종료되고, 다음 로그인에서 비밀번호를 다시
        설정하도록 요구합니다.
      </p>

      <label className="narrow">
        <span>새 비밀번호</span>
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          minLength={12}
          autoComplete="new-password"
          required
          autoFocus
        />
      </label>

      {action.error && <div className="alert">{action.error}</div>}

      <div className="form-actions">
        <button type="button" className="btn" onClick={onCancel}>
          취소
        </button>
        <button type="submit" className="btn btn-primary" disabled={action.busy}>
          {action.busy ? "적용 중…" : "재설정"}
        </button>
      </div>
    </form>
  );
}

function RenameUserForm({
  target,
  onDone,
  onCancel,
}: {
  target: User;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [username, setUsername] = useState(target.username);
  const action = useAction();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await action.run(() => api.renameUser(target.id, username.trim().toLowerCase()))) {
      onDone();
    }
  };

  return (
    <form className="card form" onSubmit={submit}>
      <h2>
        <code>{target.username}</code> 아이디 변경
      </h2>
      <label className="narrow">
        <span>새 아이디</span>
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value.toLowerCase())}
          autoComplete="username"
          required
          autoFocus
        />
        <small className="muted">영문 소문자·숫자·. - _ · 2–32자</small>
      </label>
      {action.error && <div className="alert">{action.error}</div>}
      <div className="form-actions">
        <button type="button" className="btn" onClick={onCancel}>
          취소
        </button>
        <button type="submit" className="btn btn-primary" disabled={action.busy}>
          {action.busy ? "변경 중…" : "변경"}
        </button>
      </div>
    </form>
  );
}
