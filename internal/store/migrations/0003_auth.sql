-- Session tracking and audit detail needed by the login flow.

-- Refreshed on every authenticated request so an idle session can be expired
-- separately from an absolute lifetime. Without it, a session left open on an
-- unattended machine stays valid for its full duration.
ALTER TABLE sessions ADD COLUMN last_seen_at TEXT;

-- Sessions are looked up by the hash of the cookie value, and every request
-- does that lookup, so it needs to be indexed. (sessions.id already holds the
-- hash; this index covers the user_id direction, used when revoking every
-- session belonging to an account.)
CREATE INDEX idx_sessions_user ON sessions (user_id);

-- The login rate limiter counts recent failures per username and per source
-- address. Keeping it in the database rather than in memory means a restart
-- does not reset an attacker's budget, and a service that crash-loops cannot
-- be used to wipe the counter.
CREATE TABLE login_failures (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL,
    remote   TEXT NOT NULL,
    at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_login_failures_at ON login_failures (at);
CREATE INDEX idx_login_failures_username ON login_failures (username, at);
