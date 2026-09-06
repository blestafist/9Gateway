CREATE TABLE usage_buckets (
    api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    bucket_start INTEGER NOT NULL
        CHECK (typeof(bucket_start) = 'integer' AND bucket_start >= 0),
    bucket_seconds INTEGER NOT NULL
        CHECK (typeof(bucket_seconds) = 'integer' AND bucket_seconds > 0),
    committed_tokens INTEGER NOT NULL
        CHECK (typeof(committed_tokens) = 'integer' AND committed_tokens >= 0),
    created_at INTEGER NOT NULL
        CHECK (typeof(created_at) = 'integer' AND created_at >= 0),
    updated_at INTEGER NOT NULL
        CHECK (typeof(updated_at) = 'integer' AND updated_at >= created_at),
    PRIMARY KEY (api_key_id, bucket_start, bucket_seconds),
    CHECK (bucket_start % bucket_seconds = 0)
);

CREATE INDEX idx_usage_buckets_key_window
    ON usage_buckets(api_key_id, bucket_seconds, bucket_start);
CREATE INDEX idx_usage_buckets_expiration
    ON usage_buckets(bucket_start, bucket_seconds);
