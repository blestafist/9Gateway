-- Version 8 keeps sensitive body captures separate from ordinary request
-- metadata so the two data classes can have independent retention and access.
-- RequestBodySchemaSafetyMaxBytes is 1048576 bytes, matching T130's
-- max_captured_body_bytes deployment maximum.
CREATE TABLE request_bodies (
    request_id TEXT NOT NULL REFERENCES requests(request_id) ON DELETE CASCADE,
    body_kind TEXT NOT NULL
        CHECK (
            typeof(body_kind) = 'text'
            AND body_kind IN ('client_request', 'upstream_request', 'response')
        ),
    body BLOB NOT NULL
        CHECK (typeof(body) = 'blob' AND length(body) <= 1048576),
    original_size INTEGER NOT NULL
        CHECK (
            typeof(original_size) = 'integer'
            AND original_size >= 0
            AND length(body) <= original_size
        ),
    truncated INTEGER NOT NULL
        CHECK (typeof(truncated) = 'integer' AND truncated IN (0, 1)),
    CHECK (
        (truncated = 1 AND length(body) < original_size)
        OR (truncated = 0 AND length(body) = original_size)
    ),
    PRIMARY KEY (request_id, body_kind)
);
