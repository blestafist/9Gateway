-- A database that already ran the original 003 migration has a fabricated
-- identity alongside its authoritative v2 row. Remove only those paired rows;
-- startup will resolve the source row against the current policy.
DELETE FROM usage_bucket_identities
WHERE EXISTS (
    SELECT 1 FROM usage_buckets
    WHERE usage_buckets.api_key_id = usage_bucket_identities.api_key_id
      AND usage_buckets.bucket_start = usage_bucket_identities.bucket_start
      AND usage_buckets.bucket_seconds = usage_bucket_identities.bucket_seconds
);

CREATE TABLE usage_bucket_migration_state (
    id INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    legacy_rows_present INTEGER NOT NULL CHECK (legacy_rows_present IN (0, 1))
);
INSERT INTO usage_bucket_migration_state(id, legacy_rows_present)
VALUES (1, CASE WHEN EXISTS (SELECT 1 FROM usage_buckets) THEN 1 ELSE 0 END);
