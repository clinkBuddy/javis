import { useState } from "react";

import { api } from "../api";
import { useAction, usePolled } from "../hooks";

export function Bans() {
  const bans = usePolled(api.listBans, 5_000);
  const action = useAction();
  const [ip, setIp] = useState("");
  const [reason, setReason] = useState("");

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    if (await action.run(() => api.createBan(ip.trim(), reason.trim() || "manual"))) {
      setIp("");
      setReason("");
      bans.refresh();
    }
  };

  const remove = async (addr: string) => {
    if (await action.run(() => api.deleteBan(addr))) {
      bans.refresh();
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>차단 IP</h1>
          <p className="muted small">
            스캐너 경로 접근과 연속 로그인 실패 시 해당 주소는 응답 없이 연결이 끊깁니다.
            루프백(127.0.0.1)은 자동·수동 모두 차단되지 않습니다.
          </p>
        </div>
      </div>

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}
      {bans.error && <div className="alert">{bans.error}</div>}

      <form className="card form" onSubmit={add}>
        <h2>수동 차단</h2>
        <div className="grid-2">
          <label>
            <span>IP</span>
            <input value={ip} onChange={(e) => setIp(e.target.value)} placeholder="203.0.113.10" required />
          </label>
          <label>
            <span>사유 (선택)</span>
            <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="의심 트래픽" />
          </label>
        </div>
        <div className="form-actions">
          <button type="submit" className="btn btn-primary" disabled={action.busy}>
            차단
          </button>
        </div>
      </form>

      {bans.data && bans.data.length === 0 && <p className="muted">차단된 주소가 없습니다.</p>}

      {bans.data && bans.data.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>IP</th>
              <th>사유</th>
              <th>등록</th>
              <th>주체</th>
              <th className="right" />
            </tr>
          </thead>
          <tbody>
            {bans.data.map((b) => (
              <tr key={b.ip}>
                <td className="mono">{b.ip}</td>
                <td className="small">{b.reason}</td>
                <td className="small nowrap">{b.createdAt}</td>
                <td className="small">{b.createdBy}</td>
                <td className="right">
                  <button className="btn btn-sm" disabled={action.busy} onClick={() => void remove(b.ip)}>
                    해제
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
