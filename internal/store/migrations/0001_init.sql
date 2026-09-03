-- Core schema for JARVIS.
--
-- Timestamps are stored as ISO-8601 UTC text (datetime('now')) so the database
-- stays readable with any sqlite CLI. Metric timestamps are unix seconds
-- because they are queried by range and never read by hand.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    totp_secret   TEXT,
    disabled      INTEGER NOT NULL DEFAULT 0,
    must_change   INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    last_login_at TEXT
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at  TEXT NOT NULL,
    remote_addr TEXT,
    user_agent  TEXT
);

CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- Registered JDKs. One host can run apps on different Java majors.
CREATE TABLE jdks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    java_home   TEXT NOT NULL,
    java_exe    TEXT NOT NULL,
    vendor      TEXT,
    version     TEXT,
    major       INTEGER,
    is_default  INTEGER NOT NULL DEFAULT 0,
    detected_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- An "app" is the logical server name the administrator assigns.
CREATE TABLE apps (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    display_name TEXT,
    description  TEXT,

    -- 'local' runs java.exe on this host. 'remote' is the P6 SSH runner.
    target_kind TEXT NOT NULL DEFAULT 'local' CHECK (target_kind IN ('local', 'remote')),

    work_dir    TEXT,
    run_as_user TEXT,

    -- Boot ordering. depends_on is a JSON array of app names.
    autostart       INTEGER NOT NULL DEFAULT 0,
    start_order     INTEGER NOT NULL DEFAULT 100,
    depends_on      TEXT NOT NULL DEFAULT '[]',
    start_delay_sec INTEGER NOT NULL DEFAULT 0,

    -- Graceful stop budget before TerminateProcess.
    stop_timeout_sec INTEGER NOT NULL DEFAULT 30,
    shutdown_url     TEXT,

    -- Crash watchdog.
    watchdog     INTEGER NOT NULL DEFAULT 1,
    max_restarts INTEGER NOT NULL DEFAULT 5,

    -- Readiness probe used by start and by rollback decisions.
    health_kind        TEXT NOT NULL DEFAULT 'none' CHECK (health_kind IN ('none', 'tcp', 'http')),
    health_target      TEXT,
    health_timeout_sec INTEGER NOT NULL DEFAULT 120,
    actuator_base_url  TEXT,

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Uploaded jars. Immutable once stored; sha256 is verified before every launch.
CREATE TABLE artifacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id     INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    version    TEXT NOT NULL,
    file_name  TEXT NOT NULL,
    rel_path   TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    sha256     TEXT NOT NULL,

    -- Parsed from BOOT-INF / META-INF/MANIFEST.MF at upload time.
    manifest_json          TEXT,
    start_class            TEXT,
    spring_boot_version    TEXT,
    implementation_version TEXT,
    build_jdk              TEXT,

    uploaded_by INTEGER REFERENCES users (id),
    uploaded_at TEXT NOT NULL DEFAULT (datetime('now')),
    notes       TEXT,

    UNIQUE (app_id, version)
);

-- Launch configuration, versioned so a bad change can be reverted.
CREATE TABLE jvm_profiles (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id   INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    jdk_id   INTEGER REFERENCES jdks (id),

    heap_min TEXT,
    heap_max TEXT,
    gc       TEXT,

    -- JSON: array, array, object.
    jvm_args     TEXT NOT NULL DEFAULT '[]',
    program_args TEXT NOT NULL DEFAULT '[]',
    env          TEXT NOT NULL DEFAULT '{}',

    active     INTEGER NOT NULL DEFAULT 0,
    note       TEXT,
    created_by INTEGER REFERENCES users (id),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    UNIQUE (app_id, revision)
);

CREATE INDEX idx_jvm_profiles_active ON jvm_profiles (app_id, active);

-- Exactly one row per app: the current or last known process.
--
-- process_create_time is the Windows FILETIME of the process. It is what makes
-- re-attachment safe: a recycled PID will not match it.
CREATE TABLE instances (
    app_id INTEGER PRIMARY KEY REFERENCES apps (id) ON DELETE CASCADE,
    state  TEXT NOT NULL DEFAULT 'STOPPED',

    instance_uuid       TEXT,
    pid                 INTEGER,
    process_create_time INTEGER,

    artifact_id  INTEGER REFERENCES artifacts (id),
    profile_id   INTEGER REFERENCES jvm_profiles (id),
    command_line TEXT,
    console_log  TEXT,

    started_at    TEXT,
    stopped_at    TEXT,
    exit_code     INTEGER,
    restart_count INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT,
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Port registry. The UNIQUE constraint is the whole point: it stops two apps
-- from being configured onto the same port on a single host.
CREATE TABLE ports (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id  INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    port    INTEGER NOT NULL UNIQUE,
    purpose TEXT
);

CREATE TABLE events (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id   INTEGER REFERENCES apps (id) ON DELETE CASCADE,
    kind     TEXT NOT NULL,
    severity TEXT NOT NULL DEFAULT 'info',
    message  TEXT,
    detail   TEXT,
    at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_events_app_at ON events (app_id, at DESC);

-- Per-process metrics. resolution is 'raw' | '1m' | '5m'; older resolutions are
-- produced by a rollup job and the source rows are then pruned.
CREATE TABLE metric_samples (
    app_id     INTEGER NOT NULL,
    resolution TEXT NOT NULL DEFAULT 'raw',
    ts         INTEGER NOT NULL,

    cpu_percent   REAL,
    rss_bytes     INTEGER,
    private_bytes INTEGER,
    threads       INTEGER,
    handles       INTEGER,

    heap_used    INTEGER,
    heap_max     INTEGER,
    nonheap_used INTEGER,
    gc_count     INTEGER,
    gc_time_ms   INTEGER,

    PRIMARY KEY (app_id, resolution, ts)
) WITHOUT ROWID;

-- Host-wide metrics, so an operator can tell a noisy app from a saturated box.
CREATE TABLE host_samples (
    resolution TEXT NOT NULL DEFAULT 'raw',
    ts         INTEGER NOT NULL,

    cpu_percent   REAL,
    mem_total     INTEGER,
    mem_used      INTEGER,
    swap_used     INTEGER,
    disk_total    INTEGER,
    disk_free     INTEGER,
    load_procs    INTEGER,

    PRIMARY KEY (resolution, ts)
) WITHOUT ROWID;

CREATE TABLE alert_rules (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id       INTEGER REFERENCES apps (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    metric       TEXT NOT NULL,
    op           TEXT NOT NULL CHECK (op IN ('>', '>=', '<', '<=', '==', '!=')),
    threshold    REAL,
    for_sec      INTEGER NOT NULL DEFAULT 60,
    severity     TEXT NOT NULL DEFAULT 'warning',
    channels     TEXT NOT NULL DEFAULT '[]',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE alerts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id     INTEGER REFERENCES alert_rules (id) ON DELETE SET NULL,
    app_id      INTEGER REFERENCES apps (id) ON DELETE CASCADE,
    severity    TEXT NOT NULL DEFAULT 'warning',
    message     TEXT NOT NULL,
    value       REAL,
    fired_at    TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at TEXT,
    notified_at TEXT
);

CREATE INDEX idx_alerts_open ON alerts (resolved_at, fired_at DESC);

-- Append-only. Nothing in the application deletes from this table.
CREATE TABLE audit_logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          TEXT NOT NULL DEFAULT (datetime('now')),
    user_id     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    username    TEXT,
    remote_addr TEXT,
    action      TEXT NOT NULL,
    target      TEXT,
    result      TEXT NOT NULL DEFAULT 'ok',
    detail      TEXT
);

CREATE INDEX idx_audit_at ON audit_logs (at DESC);
