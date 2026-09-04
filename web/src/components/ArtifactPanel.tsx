import { useRef, useState } from "react";

import { api, formatBytes, uploadArtifact } from "../api";
import { useAction, usePolled } from "../hooks";

interface Props {
  appName: string;
  running: boolean;
  runningVersion?: string;
  onChanged: () => void;
}

export function ArtifactPanel({ appName, running, runningVersion, onChanged }: Props) {
  const artifacts = usePolled(() => api.listArtifacts(appName), 15_000);
  const action = useAction();

  const refreshAll = () => {
    artifacts.refresh();
    onChanged();
  };

  const act = async (fn: () => Promise<unknown>) => {
    if (await action.run(fn)) {
      refreshAll();
    }
  };

  return (
    <section className="card">
      <h2>jar 저장소</h2>
      <p className="muted small">
        업로드한 jar는 JARVIS 저장소에 버전별로 보관됩니다. 기동에 쓸 버전을
        <strong> 승격</strong>하면 다음 기동부터 적용됩니다.
      </p>

      <UploadForm appName={appName} onUploaded={refreshAll} />

      {action.error && (
        <div className="alert" onClick={action.clearError}>
          {action.error}
        </div>
      )}
      {artifacts.error && <div className="alert">{artifacts.error}</div>}

      {artifacts.data?.length === 0 && (
        <p className="muted">아직 업로드된 jar가 없습니다.</p>
      )}

      {artifacts.data && artifacts.data.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>버전</th>
              <th>파일</th>
              <th>크기</th>
              <th>Spring Boot</th>
              <th>Build JDK</th>
              <th>업로드</th>
              <th className="right" />
            </tr>
          </thead>
          <tbody>
            {artifacts.data.map((a) => {
              const isRunning = running && a.version === runningVersion;
              return (
                <tr key={a.id}>
                  <td className="mono">
                    {a.version}
                    {a.active && <span className="pill pill-ok">승격</span>}
                    {isRunning && <span className="pill">실행 중</span>}
                  </td>
                  <td className="mono small" title={a.sha256}>
                    {a.fileName}
                    {a.startClass && <div className="muted small">{a.startClass}</div>}
                  </td>
                  <td className="nowrap">{formatBytes(a.sizeBytes)}</td>
                  <td className="mono small">{a.springBootVersion || "—"}</td>
                  <td className="mono small">{a.buildJdk || "—"}</td>
                  <td className="small nowrap">{a.uploadedAt}</td>
                  <td className="right nowrap">
                    {!a.active && (
                      <button
                        className="btn btn-sm"
                        disabled={action.busy}
                        onClick={() => act(() => api.activateArtifact(appName, a.version))}
                      >
                        승격
                      </button>
                    )}
                    <button
                      className="btn btn-sm btn-danger"
                      disabled={action.busy || a.active || isRunning}
                      title={
                        a.active
                          ? "승격된 버전은 삭제할 수 없습니다"
                          : isRunning
                            ? "실행 중인 버전은 삭제할 수 없습니다"
                            : undefined
                      }
                      onClick={() => act(() => api.deleteArtifact(appName, a.version))}
                    >
                      삭제
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}

function UploadForm({ appName, onUploaded }: { appName: string; onUploaded: () => void }) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [version, setVersion] = useState("");
  const [notes, setNotes] = useState("");
  const [activate, setActivate] = useState(true);
  const [percent, setPercent] = useState<number | null>(null);
  const [error, setError] = useState<string | undefined>();

  const pick = (picked: File | null) => {
    setFile(picked);
    setError(undefined);
    // Most build pipelines put the version in the filename, so offering it as
    // a default saves retyping it and keeps the repository consistent with
    // what the artifact actually is.
    if (picked && !version) {
      const guessed = /-(\d[\w.]*?)(?:-SNAPSHOT)?\.jar$/i.exec(picked.name);
      if (guessed) setVersion(guessed[1]);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!file) return;

    setPercent(0);
    setError(undefined);
    try {
      await uploadArtifact(appName, { file, version: version.trim(), notes, activate }, setPercent);
      setFile(null);
      setVersion("");
      setNotes("");
      if (fileInput.current) fileInput.current.value = "";
      onUploaded();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setPercent(null);
    }
  };

  return (
    <form className="upload" onSubmit={submit}>
      <div className="upload-row">
        <input
          ref={fileInput}
          type="file"
          accept=".jar"
          onChange={(e) => pick(e.target.files?.[0] ?? null)}
          required
        />
        <input
          className="version-input"
          value={version}
          onChange={(e) => setVersion(e.target.value)}
          placeholder="버전 (예: 0.1.0)"
          required
        />
        <input
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          placeholder="메모 (선택)"
        />
        <label className="check">
          <input
            type="checkbox"
            checked={activate}
            onChange={(e) => setActivate(e.target.checked)}
          />
          바로 승격
        </label>
        <button className="btn btn-primary" type="submit" disabled={percent !== null || !file}>
          업로드
        </button>
      </div>

      {percent !== null && (
        <div className="progress">
          <div className="progress-bar" style={{ width: `${percent}%` }} />
          <span className="progress-label">{percent}%</span>
        </div>
      )}
      {error && <div className="alert">{error}</div>}
    </form>
  );
}
