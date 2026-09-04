CREATE TABLE api_keys (
    id TEXT NOT NULL PRIMARY KEY CHECK (length(trim(id)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    prefix TEXT NOT NULL CHECK (length(trim(prefix)) > 0),
    key_hash BLOB NOT NULL UNIQUE CHECK (length(key_hash) = 32),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    expires_at INTEGER NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    policy_json TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX idx_api_keys_active ON api_keys(enabled, expires_at) WHERE enabled = 1;
