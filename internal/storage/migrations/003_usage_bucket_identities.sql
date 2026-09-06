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

-- T098 rows did not retain the policy amount. Preserve their committed usage
-- conservatively under a distinct identity; startup validates it against the
-- current policy before importing it into the live limiter.
INSERT INTO usage_bucket_identities
    (api_key_id, bucket_start, bucket_seconds, bucket_amount, committed_tokens, created_at, updated_at)
SELECT api_key_id, bucket_start, bucket_seconds, bucket_seconds, committed_tokens, created_at, updated_at
FROM usage_buckets;
