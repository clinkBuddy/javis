// Typed wrapper over the JARVIS REST API.

const BASE = "/api/v1";

export type AppState =
  | "STOPPED"
  | "STARTING"
  | "RUNNING"
  | "STOPPING"
  | "FAILED"
  | "ORPHAN";

export interface App {
  id: number;
  name: string;
  displayName: string;
  description: string;
  targetKind: string;
  shutdownUrl?: string;
  autostart: boolean;
  watchdog: boolean;
  createdAt: string;
  updatedAt: string;

  state: AppState;
  pid?: number;
  startedAt?: string;
  runningVersion?: string;
  activeVersion?: string;
  artifactCount: number;
}

export interface AppStatus {
  app: string;
  state: AppState;
  pid?: number;
  instance?: string;
  consoleLog?: string;
  commandLine?: string;
  runningVersion?: string;
  runningRevision?: string;
}

export interface Artifact {
  id: number;
  version: string;
  fileName: string;
  relPath: string;
  sizeBytes: number;
  sha256: string;
  startClass?: string;
  springBootVersion?: string;
  implementationVersion?: string;
  buildJdk?: string;
  notes?: string;
  uploadedAt: string;
  active: boolean;
}

export interface Profile {
  id: number;
  revision: number;
  jdkId?: number;
  jdkName?: string;
  heapMin?: string;
  heapMax?: string;
  gc?: string;
  jvmArgs: string[];
  programArgs: string[];
  env: Record<string, string>;
  active: boolean;
  note?: string;
  createdAt: string;
}

export interface ProfileUpdate {
  jdkId: number | null;
  heapMin: string;
  heapMax: string;
  gc: string;
  jvmArgs: string[];
  programArgs: string[];
  env: Record<string, string>;
  note: string;
}

export interface Jdk {
  id: number;
  name: string;
  javaHome: string;
  javaExe: string;
  vendor?: string;
  version?: string;
  major: number;
  isDefault: boolean;
}

export interface Preview {
  app: string;
  javaExe: string;
  args: string[];
  workDir: string;
  commandLine: string;
}

export interface Health {
  status: string;
  version: string;
  dataRoot: string;
  uptime: string;
  database: string;
}

/** ApiError carries the server's message so the UI never has to show a bare
 *  status code. Nearly every 4xx from this API is an explanation of what is
 *  wrong with the request, and that text is the most useful thing to display. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      ...(init?.body && !(init.body instanceof FormData)
        ? { "Content-Type": "application/json" }
        : {}),
      ...init?.headers,
    },
  });

  if (!res.ok) {
    throw new ApiError(res.status, await errorMessage(res));
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: string };
    if (body.error) {
      return body.error;
    }
  } catch {
    // A non-JSON body means the failure happened before a handler ran, e.g. a
    // panic caught by the recoverer.
  }
  return `${res.status} ${res.statusText}`;
}

export const api = {
  health: () => request<Health>("/health"),

  listApps: () => request<App[]>("/apps"),
  getApp: (name: string) => request<App>(`/apps/${encodeURIComponent(name)}`),
  createApp: (body: {
    name: string;
    displayName?: string;
    description?: string;
    shutdownUrl?: string;
  }) =>
    request<{ id: number; name: string }>("/apps", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  deleteApp: (name: string) =>
    request<{ deleted: string }>(`/apps/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),

  start: (name: string) =>
    request<AppStatus>(`/apps/${encodeURIComponent(name)}/start`, { method: "POST" }),
  stop: (name: string) =>
    request<{ app: string; method: string; duration: string }>(
      `/apps/${encodeURIComponent(name)}/stop`,
      { method: "POST" },
    ),
  restart: (name: string) =>
    request<AppStatus>(`/apps/${encodeURIComponent(name)}/restart`, { method: "POST" }),
  status: (name: string) =>
    request<AppStatus>(`/apps/${encodeURIComponent(name)}/status`),

  listArtifacts: (name: string) =>
    request<Artifact[]>(`/apps/${encodeURIComponent(name)}/artifacts`),
  activateArtifact: (name: string, version: string) =>
    request<{ restartNeeded: boolean }>(
      `/apps/${encodeURIComponent(name)}/artifacts/${encodeURIComponent(version)}/activate`,
      { method: "POST" },
    ),
  deleteArtifact: (name: string, version: string) =>
    request<{ deleted: string }>(
      `/apps/${encodeURIComponent(name)}/artifacts/${encodeURIComponent(version)}`,
      { method: "DELETE" },
    ),

  getProfile: (name: string) =>
    request<Profile>(`/apps/${encodeURIComponent(name)}/profile`),
  listProfiles: (name: string) =>
    request<Profile[]>(`/apps/${encodeURIComponent(name)}/profiles`),
  updateProfile: (name: string, body: ProfileUpdate) =>
    request<{ revision: number; restartNeeded: boolean }>(
      `/apps/${encodeURIComponent(name)}/profile`,
      { method: "PUT", body: JSON.stringify(body) },
    ),
  activateProfile: (name: string, revision: number) =>
    request<{ revision: number; restartNeeded: boolean }>(
      `/apps/${encodeURIComponent(name)}/profiles/${revision}/activate`,
      { method: "POST" },
    ),
  preview: (name: string) =>
    request<Preview>(`/apps/${encodeURIComponent(name)}/preview`),

  listJdks: () => request<Jdk[]>("/jdks"),
  scanJdks: () => request<{ found: number; added: Jdk[] }>("/jdks/scan", { method: "POST" }),
  setDefaultJdk: (id: number) =>
    request<{ default: number }>(`/jdks/${id}/default`, { method: "POST" }),
  deleteJdk: (id: number) => request<{ deleted: string }>(`/jdks/${id}`, { method: "DELETE" }),
};

/** uploadArtifact reports progress, so it uses XMLHttpRequest rather than
 *  fetch: a Spring Boot fat jar is tens of megabytes and an upload with no
 *  visible progress is indistinguishable from one that has stalled. */
export function uploadArtifact(
  appName: string,
  opts: { file: File; version: string; notes?: string; activate?: boolean },
  onProgress?: (percent: number) => void,
): Promise<Artifact & { manifest?: unknown }> {
  const form = new FormData();
  form.append("file", opts.file);
  form.append("version", opts.version);
  if (opts.notes) form.append("notes", opts.notes);
  if (opts.activate) form.append("activate", "true");

  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${BASE}/apps/${encodeURIComponent(appName)}/artifacts`);

    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable && onProgress) {
        onProgress(Math.round((e.loaded / e.total) * 100));
      }
    });

    xhr.addEventListener("load", () => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(xhr.responseText);
      } catch {
        parsed = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(parsed as Artifact);
        return;
      }
      const message =
        (parsed as { error?: string } | undefined)?.error ??
        `${xhr.status} ${xhr.statusText}`;
      reject(new ApiError(xhr.status, message));
    });

    xhr.addEventListener("error", () =>
      reject(new ApiError(0, "the upload could not reach JARVIS")),
    );
    xhr.addEventListener("abort", () => reject(new ApiError(0, "upload cancelled")));

    xhr.send(form);
  });
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}
