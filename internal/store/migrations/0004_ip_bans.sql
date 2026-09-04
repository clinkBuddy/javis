-- IP bans for the admin interface when it is reachable from a network.

CREATE TABLE ip_bans (
    ip         TEXT PRIMARY KEY,
    reason     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    created_by TEXT NOT NULL DEFAULT 'auto'
);
