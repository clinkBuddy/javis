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
  maxRestarts: number;
  startOrder: number;
  startDelaySec: number;
  stopTimeoutSec: number;
  dependsOn: string[];
  createdAt: string;
  updatedAt: string;

  state: AppState;
  pid?: number;
  startedAt?: string;
  runningVersion?: string;
  activeVersion?: string;
  artifactCount: number;
  restartCount: number;
	cpuPercent?: number;
	rssBytes?: number;
	threads?: number;
	handles?: number;
	privateBytes?: number;
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
  restartCount?: number;
  cpuPercent?: number;
  rssBytes?: number;
  threads?: number;
  handles?: number;
}

export interface ProcessReading {
  appId?: number;
  appName?: string;
  ts: number;
  cpuPercent: number;
  rssBytes: number;
  privateBytes: number;
  threads: number;
  handles: number;
  pid?: number;
}

export interface HostReading {
  ts: number;
  cpuPercent: number;
  memTotal: number;
  memUsed: number;
  swapUsed: number;
  diskTotal: number;
  diskFree: number;
  loadProcs: number;
}

export interface AppUpdate {
  displayName: string;
  description: string;
  shutdownUrl: string;
  autostart: boolean;
  watchdog: boolean;
  maxRestarts: number;
  startOrder: number;
  startDelaySec: number;
  stopTimeoutSec: number;
  dependsOn: string[];
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

export type Role = "admin" | "operator" | "viewer";

export interface User {
  id: number;
  username: string;
  role: Role;
  disabled: boolean;
  mustChange: boolean;
  createdAt: string;
  lastLoginAt?: string;
}

export interface Me extends User {
  csrfToken: string;
}

export interface Ban {
  ip: string;
  reason: string;
  createdAt: string;
  createdBy: string;
}

export interface HTTPExchange {
  at: string;
  method: string;
  path: string;
  status: number;
  ms: number;
  remote?: string;
}

export interface TrafficPoint {
  ts: number;
  tps: number;
  active: number;
  avgMs: number;
  errors: number;
}

export interface TrafficSnapshot {
  appId: number;
  appName: string;
  source: string;
  active: number;
  listen?: number[];
  tps: number;
  errorRate: number;
  avgMs: number;
  maxMs: number;
  history: TrafficPoint[];
  recent: HTTPExchange[];
}

export interface MetricsOverview {
  host: { latest: HostReading; history: HostReading[] };
  apps: {
    latest: Record<string, ProcessReading>;
    history: Record<string, ProcessReading[]>;
  };
  traffic?: Record<string, TrafficSnapshot>;
}

export interface AuditEntry {
  id: number;
  at: string;
  username: string;
  remoteAddr: string;
  action: string;
  target: string;
  result: string;
  detail: string;
}

const ROLE_RANK: Record<Role, number> = { viewer: 1, operator: 2, admin: 3 };

/** Mirrors the server's role check so the UI can hide controls it knows will
 *  be refused. This is presentation only — the server enforces the same rule
 *  and is the thing that actually decides. */
export function atLeast(have: Role | undefined, want: Role): boolean {
  return have ? ROLE_RANK[have] >= ROLE_RANK[want] : false;
}

/** ApiError carries the server's message so the UI never has to show a bare
 *  status code. Nearly every 4xx from this API is an explanation of what is
 *  wrong with the request, and that text is the most useful thing to display.
 *
 *  `code` is the server's stable identifier for the few failures the UI has to
 *  react to structurally, rather than just display. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, message: string, code = "") {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

/** The CSRF token accompanies every mutating request in a header. A
 *  cross-origin page can make the browser send the cookie but cannot read it
 *  to set the header, which is what makes the pair meaningful. */
let csrfToken = readCsrfCookie();

function readCsrfCookie(): string {
  const match = /(?:^|;\s*)jarvis_csrf=([^;]+)/.exec(document.cookie);
  return match ? decodeURIComponent(match[1]) : "";
}

export function setCsrfToken(token: string) {
  csrfToken = token;
}

/** Handlers registered here are called when the server says the session is
 *  gone, so a single expiry bounces the whole UI to the login screen instead
 *  of leaving every panel showing its own 401. */
type SessionListener = () => void;
const sessionListeners = new Set<SessionListener>();

export function onSessionLost(fn: SessionListener): () => void {
  sessionListeners.add(fn);
  return () => sessionListeners.delete(fn);
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const mutating = !!init?.method && init.method !== "GET";

  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      ...(init?.body && !(init.body instanceof FormData)
        ? { "Content-Type": "application/json" }
        : {}),
      ...(mutating && csrfToken ? { "X-JARVIS-CSRF": csrfToken } : {}),
      ...init?.headers,
    },
  });

  if (!res.ok) {
    const { message, code } = await errorBody(res);
    if (res.status === 401 && path !== "/auth/login") {
      sessionListeners.forEach((fn) => fn());
    }
    throw new ApiError(res.status, message, code);
  }
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

async function errorBody(res: Response): Promise<{ message: string; code: string }> {
  try {
    const body = (await res.json()) as { error?: string; code?: string };
    if (body.error) {
      return { message: body.error, code: body.code ?? "" };
    }
  } catch {
    // A non-JSON body means the failure happened before a handler ran, e.g. a
    // panic caught by the recoverer.
  }
  return { message: `${res.status} ${res.statusText}`, code: "" };
}

export const api = {
  health: () => request<Health>("/health"),
  authSetup: () => request<{ defaultHint: boolean }>("/auth/setup"),

  login: (username: string, password: string) =>
    request<Me>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  me: () => request<Me>("/auth/me"),
  logout: () => request<{ status: string }>("/auth/logout", { method: "POST" }),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<{ status: string }>("/auth/password", {
      method: "POST",
      body: JSON.stringify({ currentPassword, newPassword }),
    }),
  changeUsername: (username: string) =>
    request<{ username: string }>("/auth/username", {
      method: "POST",
      body: JSON.stringify({ username }),
    }),

  listUsers: () => request<User[]>("/users"),
  createUser: (username: string, password: string, role: Role) =>
    request<User>("/users", {
      method: "POST",
      body: JSON.stringify({ username, password, role }),
    }),
  updateUser: (id: number, role: Role, disabled: boolean) =>
    request<{ id: number }>(`/users/${id}`, {
      method: "PUT",
      body: JSON.stringify({ role, disabled }),
    }),
  resetUserPassword: (id: number, newPassword: string) =>
    request<{ id: number }>(`/users/${id}/password`, {
      method: "POST",
      body: JSON.stringify({ newPassword }),
    }),
  renameUser: (id: number, username: string) =>
    request<{ id: number; username: string }>(`/users/${id}/username`, {
      method: "POST",
      body: JSON.stringify({ username }),
    }),
  deleteUser: (id: number) => request<{ deleted: number }>(`/users/${id}`, { method: "DELETE" }),

  listAudit: (limit = 200) => request<AuditEntry[]>(`/audit?limit=${limit}`),

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
  updateApp: (name: string, body: AppUpdate) =>
    request<{ updated: boolean }>(`/apps/${encodeURIComponent(name)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),

  appMetrics: (name: string, minutes = 60) =>
    request<{ latest: ProcessReading; history: ProcessReading[] }>(
      `/apps/${encodeURIComponent(name)}/metrics?minutes=${minutes}`,
    ),
  appTraffic: (name: string) =>
    request<TrafficSnapshot>(`/apps/${encodeURIComponent(name)}/traffic`),
  hostNow: () => request<HostReading>("/host"),
  hostMetrics: (minutes = 60) =>
    request<HostReading[]>(`/host/metrics?minutes=${minutes}`),
  metricsOverview: (minutes = 60) =>
    request<MetricsOverview>(`/metrics/overview?minutes=${minutes}`),
  listBans: () => request<Ban[]>("/bans"),
  createBan: (ip: string, reason?: string) =>
    request<{ ip: string }>("/bans", {
      method: "POST",
      body: JSON.stringify({ ip, reason }),
    }),
  deleteBan: (ip: string) =>
    request<{ unbanned: string }>(`/bans/${encodeURIComponent(ip)}`, { method: "DELETE" }),
  appLogs: (name: string, tail = 200) =>
    request<{ path: string; size: number; lines: string[] }>(
      `/apps/${encodeURIComponent(name)}/logs?tail=${tail}`,
    ),

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
    if (csrfToken) {
      xhr.setRequestHeader("X-JARVIS-CSRF", csrfToken);
    }

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
      const body = parsed as { error?: string; code?: string } | undefined;
      if (xhr.status === 401) {
        sessionListeners.forEach((fn) => fn());
      }
      reject(
        new ApiError(
          xhr.status,
          body?.error ?? `${xhr.status} ${xhr.statusText}`,
          body?.code ?? "",
        ),
      );
    });

    xhr.addEventListener("error", () =>
      reject(new ApiError(0, "the upload could not reach JARVIS")),
    );
    xhr.addEventListener("abort", () => reject(new ApiError(0, "upload cancelled")));

    xhr.send(form);
  });
}

export function formatDuration(startedAt: string | undefined): string {
  if (!startedAt) return "—";
  const start = Date.parse(startedAt.replace(" ", "T") + "Z");
  if (Number.isNaN(start)) return "—";
  const sec = Math.max(0, Math.floor((Date.now() - start) / 1000));
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h > 48) return `${Math.floor(h / 24)}일`;
  if (h > 0) return `${h}시간 ${m}분`;
  if (m > 0) return `${m}분`;
  return `${sec}초`;
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
