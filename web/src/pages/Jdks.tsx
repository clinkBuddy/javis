import { api } from "../api";
import { useAction, usePolled } from "../hooks";

export function Jdks() {
  const jdks = usePolled(api.listJdks, 30_000);
  const action = useAction();

  const act = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      jdks.refresh();
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <h1>JDK</h1>
        <button
          className="btn btn-primary"
          disabled={action.busy}
          onClick={() => act(api.scanJdks)}
        >
          {action.busy ? "검색 중…" : "이 PC에서 검색"}
        </button>
      </div>

      <p className="muted small">
        기본 JDK는 앱 프로파일에서 JDK를 지정하지 않았을 때 사용됩니다. JARVIS는
        PATH의 런처가 스텁이어도 실제 설치 경로의 <code>java.exe</code>로
        바꿔서 실행합니다.
      </p>

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}
      {jdks.error && <div className="alert">{jdks.error}</div>}

      {jdks.data?.length === 0 && (
        <div className="empty">
          <p>등록된 JDK가 없습니다.</p>
          <p className="muted">“이 PC에서 검색”을 눌러 설치된 JDK를 찾으세요.</p>
        </div>
      )}

      {jdks.data && jdks.data.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>이름</th>
              <th>Java</th>
              <th>벤더</th>
              <th>경로</th>
              <th className="right" />
            </tr>
          </thead>
          <tbody>
            {jdks.data.map((j) => (
              <tr key={j.id}>
                <td>
                  {j.name}
                  {j.isDefault && <span className="pill pill-ok">기본</span>}
                </td>
                <td className="mono">{j.version}</td>
                <td className="small">{j.vendor}</td>
                <td className="mono small" title={j.javaExe}>
                  {j.javaHome}
                </td>
                <td className="right nowrap">
                  {!j.isDefault && (
                    <>
                      <button
                        className="btn btn-sm"
                        disabled={action.busy}
                        onClick={() => act(() => api.setDefaultJdk(j.id))}
                      >
                        기본으로
                      </button>
                      <button
                        className="btn btn-sm btn-danger"
                        disabled={action.busy}
                        onClick={() => act(() => api.deleteJdk(j.id))}
                      >
                        삭제
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
