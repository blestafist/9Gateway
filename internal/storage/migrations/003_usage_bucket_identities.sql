CREATE TABLE usage_bucket_identities (
    api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    bucket_start INTEGER NOT NULL CHECK (typeof(bucket_start) = 'integer' AND bucket_start >= 0),
    bucket_seconds INTEGER NOT NULL CHECK (typeof(bucket_seconds) = 'integer' AND bucket_seconds > 0),
    bucket_amount INTEGER NOT NULL CHECK (typeof(bucket_amount) = 'integer' AND bucket_amount > 0),
    committed_tokens INTEGER NOT NULL CHECK (typeof(committed_tokens) = 'integer' AND committed_tokens >= 0),
    created_at INTEGER NOT NULL CHECK (typeof(created_at) = 'integer' AND created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (typeof(updated_at) = 'integer' AND updated_at >= created_at),
    PRIMARY KEY (api_key_id, bucket_start, bucket_seconds, bucket_amount),
    CHECK (bucket_start % bucket_seconds = 0)
);
CREATE INDEX idx_usage_bucket_identities_expiration
    ON usage_bucket_identities(bucket_start, bucket_seconds);

-- Version 2 rows do not contain bucket_amount. They remain in usage_buckets
-- until startup can match their duration to exactly one current policy window.
-- Never fabricate an amount here: doing so creates a false bucket identity.
