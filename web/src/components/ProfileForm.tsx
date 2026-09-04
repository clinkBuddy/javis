import { useEffect, useState } from "react";

import { api, type Profile } from "../api";
import { useAction, usePolled } from "../hooks";

const COLLECTORS = ["", "G1", "Parallel", "Serial", "Z", "Shenandoah"];

interface Draft {
  jdkId: string;
  heapMin: string;
  heapMax: string;
  gc: string;
  jvmArgs: string;
  programArgs: string;
  env: string;
  note: string;
}

/** One argument per line rather than a single space-separated field: JVM
 *  arguments routinely contain paths with spaces, and splitting on whitespace
 *  would break them apart. */
function toDraft(p: Profile | undefined): Draft {
  return {
    jdkId: p?.jdkId ? String(p.jdkId) : "",
    heapMin: p?.heapMin ?? "",
    heapMax: p?.heapMax ?? "",
    gc: p?.gc ?? "",
    jvmArgs: (p?.jvmArgs ?? []).join("\n"),
    programArgs: (p?.programArgs ?? []).join("\n"),
    env: Object.entries(p?.env ?? {})
      .map(([k, v]) => `${k}=${v}`)
      .join("\n"),
    note: "",
  };
}

function lines(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.length > 0);
}

function parseEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of lines(text)) {
    const eq = line.indexOf("=");
    if (eq > 0) {
      out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
    }
  }
  return out;
}

export function ProfileForm({ appName, running }: { appName: string; running: boolean }) {
  const profile = usePolled(() => api.getProfile(appName), 30_000);
  const revisions = usePolled(() => api.listProfiles(appName), 30_000);
  const jdks = usePolled(api.listJdks, 60_000);
  const preview = usePolled(() => api.preview(appName), 30_000);
  const action = useAction();

  const [draft, setDraft] = useState<Draft>(() => toDraft(undefined));
  const [loadedRevision, setLoadedRevision] = useState<number | null>(null);
  const [saved, setSaved] = useState<string | undefined>();

  // Load the server's values once, then leave the form alone: polling into a
  // field the operator is typing in would overwrite their edit mid-keystroke.
  useEffect(() => {
    if (profile.data && profile.data.revision !== loadedRevision) {
      setDraft(toDraft(profile.data));
      setLoadedRevision(profile.data.revision);
    }
  }, [profile.data, loadedRevision]);

  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => {
    setDraft((d) => ({ ...d, [key]: value }));
    setSaved(undefined);
  };

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const ok = await action.run(async () => {
      const res = await api.updateProfile(appName, {
        jdkId: draft.jdkId ? Number(draft.jdkId) : null,
        heapMin: draft.heapMin.trim(),
        heapMax: draft.heapMax.trim(),
        gc: draft.gc,
        jvmArgs: lines(draft.jvmArgs),
        programArgs: lines(draft.programArgs),
        env: parseEnv(draft.env),
        note: draft.note.trim(),
      });
      setSaved(
        res.restartNeeded
          ? `rev ${res.revision} 저장됨 — 적용하려면 재시작하세요.`
          : `rev ${res.revision} 저장됨 — 다음 기동부터 적용됩니다.`,
      );
      return res;
    });
    if (ok) {
      setLoadedRevision(null);
      profile.refresh();
      revisions.refresh();
      preview.refresh();
    }
  };

  const rollback = async (revision: number) => {
    if (await action.run(() => api.activateProfile(appName, revision))) {
      setLoadedRevision(null);
      setSaved(`rev ${revision}로 되돌렸습니다.`);
      profile.refresh();
      revisions.refresh();
      preview.refresh();
    }
  };

  return (
    <section className="card">
      <h2>JVM 옵션</h2>
      <p className="muted small">
        저장하면 새 리비전이 만들어집니다. 이전 설정은 남아 있으므로 문제가 생기면
        되돌릴 수 있습니다.
        {running && " 실행 중인 프로세스에는 재시작 후 적용됩니다."}
      </p>

      <form className="form" onSubmit={save}>
        <div className="grid-3">
          <label>
            <span>JDK</span>
            <select value={draft.jdkId} onChange={(e) => set("jdkId", e.target.value)}>
              <option value="">
                기본 JDK
                {jdks.data?.find((j) => j.isDefault)
                  ? ` (${jdks.data.find((j) => j.isDefault)!.name})`
                  : ""}
              </option>
              {jdks.data?.map((j) => (
                <option key={j.id} value={j.id}>
                  {j.name} — Java {j.major} ({j.vendor})
                </option>
              ))}
            </select>
          </label>

          <label>
            <span>최소 힙 (-Xms)</span>
            <input
              value={draft.heapMin}
              onChange={(e) => set("heapMin", e.target.value)}
              placeholder="512m"
            />
          </label>

          <label>
            <span>최대 힙 (-Xmx)</span>
            <input
              value={draft.heapMax}
              onChange={(e) => set("heapMax", e.target.value)}
              placeholder="2g"
            />
          </label>
        </div>

        <label className="narrow">
          <span>GC</span>
          <select value={draft.gc} onChange={(e) => set("gc", e.target.value)}>
            {COLLECTORS.map((c) => (
              <option key={c} value={c}>
                {c === "" ? "JVM 기본값" : `-XX:+Use${c}GC`}
              </option>
            ))}
          </select>
        </label>

        <div className="grid-2">
          <label>
            <span>JVM 인자 (한 줄에 하나)</span>
            <textarea
              rows={6}
              value={draft.jvmArgs}
              onChange={(e) => set("jvmArgs", e.target.value)}
              placeholder={"-Dspring.profiles.active=prod\n-Dfile.encoding=UTF-8"}
              spellCheck={false}
            />
          </label>

          <label>
            <span>프로그램 인자 (한 줄에 하나)</span>
            <textarea
              rows={6}
              value={draft.programArgs}
              onChange={(e) => set("programArgs", e.target.value)}
              placeholder={"--server.port=8080"}
              spellCheck={false}
            />
          </label>
        </div>

        <label>
          <span>환경 변수 (KEY=VALUE, 한 줄에 하나)</span>
          <textarea
            rows={3}
            value={draft.env}
            onChange={(e) => set("env", e.target.value)}
            placeholder={"TZ=Asia/Seoul"}
            spellCheck={false}
          />
          <small className="muted">
            JARVIS의 환경 변수에 덮어씌워집니다. PATH 같은 기존 값은 유지됩니다.
          </small>
        </label>

        <label>
          <span>변경 메모 (선택)</span>
          <input
            value={draft.note}
            onChange={(e) => set("note", e.target.value)}
            placeholder="힙 상향 조정"
          />
        </label>

        {action.error && (
          <div className="alert" onClick={action.clearError}>
            {action.error}
          </div>
        )}
        {saved && <div className="notice">{saved}</div>}

        <div className="form-actions">
          <button
            type="button"
            className="btn"
            onClick={() => {
              setDraft(toDraft(profile.data));
              setSaved(undefined);
            }}
          >
            되돌리기
          </button>
          <button type="submit" className="btn btn-primary" disabled={action.busy}>
            {action.busy ? "저장 중…" : "저장"}
          </button>
        </div>
      </form>

      <h3>명령 미리보기</h3>
      {preview.data ? (
        <>
          <pre className="code">{preview.data.commandLine}</pre>
          <p className="muted small">
            작업 디렉터리 <code>{preview.data.workDir}</code>
          </p>
        </>
      ) : (
        <p className="muted small">
          {preview.error ?? "미리보기를 만들 수 없습니다."}
        </p>
      )}

      {revisions.data && revisions.data.length > 1 && (
        <>
          <h3>리비전 이력</h3>
          <table className="table">
            <thead>
              <tr>
                <th>rev</th>
                <th>힙</th>
                <th>GC</th>
                <th>JDK</th>
                <th>메모</th>
                <th>생성</th>
                <th className="right" />
              </tr>
            </thead>
            <tbody>
              {revisions.data.map((rev) => (
                <tr key={rev.id}>
                  <td className="mono">
                    {rev.revision}
                    {rev.active && <span className="pill pill-ok">사용 중</span>}
                  </td>
                  <td className="mono small">
                    {rev.heapMin || "—"} / {rev.heapMax || "—"}
                  </td>
                  <td className="mono small">{rev.gc || "—"}</td>
                  <td className="small">{rev.jdkName || "기본"}</td>
                  <td className="small">{rev.note || "—"}</td>
                  <td className="small nowrap">{rev.createdAt}</td>
                  <td className="right">
                    {!rev.active && (
                      <button
                        className="btn btn-sm"
                        disabled={action.busy}
                        onClick={() => rollback(rev.revision)}
                      >
                        이 설정으로
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
