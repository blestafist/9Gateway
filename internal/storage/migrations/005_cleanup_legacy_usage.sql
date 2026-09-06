-- Version 4 binaries wrote a compatibility checkpoint after migration 004.
-- It is not a second accounting source and must not survive into the
-- authoritative-only format. Genuine v2 rows have no matching identity and
-- remain for startup promotion; this also handles a v4 database that was
-- stopped after writing its checkpoint but before this migration ran.
DELETE FROM usage_buckets
WHERE EXISTS (
    SELECT 1 FROM usage_bucket_identities
    WHERE usage_bucket_identities.api_key_id = usage_buckets.api_key_id
      AND usage_bucket_identities.bucket_start = usage_buckets.bucket_start
      AND usage_bucket_identities.bucket_seconds = usage_buckets.bucket_seconds
);
UPDATE usage_bucket_migration_state
SET legacy_rows_present = CASE WHEN EXISTS (SELECT 1 FROM usage_buckets) THEN 1 ELSE 0 END
WHERE id = 1;
