-- Version 11 adds an insertion sequence which is never reused after deletion.
-- SQLite's implicit rowid may be reused when the highest row is deleted, so it
-- is not by itself a sound pagination snapshot fence.
CREATE TABLE traversal_sequences (
    table_name TEXT NOT NULL PRIMARY KEY,
    next_value INTEGER NOT NULL CHECK (next_value > 0)
);

ALTER TABLE api_keys ADD COLUMN insertion_seq INTEGER;
ALTER TABLE requests ADD COLUMN insertion_seq INTEGER;
UPDATE api_keys SET insertion_seq = rowid;
UPDATE requests SET insertion_seq = rowid;
INSERT INTO traversal_sequences(table_name, next_value)
SELECT 'api_keys', COALESCE(MAX(insertion_seq), 0) + 1 FROM api_keys;
INSERT INTO traversal_sequences(table_name, next_value)
SELECT 'requests', COALESCE(MAX(insertion_seq), 0) + 1 FROM requests;

CREATE TRIGGER api_keys_insertion_sequence
AFTER INSERT ON api_keys
BEGIN
    UPDATE traversal_sequences SET next_value = next_value + 1 WHERE table_name = 'api_keys';
    UPDATE api_keys SET insertion_seq = (SELECT next_value - 1 FROM traversal_sequences WHERE table_name = 'api_keys') WHERE rowid = NEW.rowid;
END;

CREATE TRIGGER requests_insertion_sequence
AFTER INSERT ON requests
BEGIN
    UPDATE traversal_sequences SET next_value = next_value + 1 WHERE table_name = 'requests';
    UPDATE requests SET insertion_seq = (SELECT next_value - 1 FROM traversal_sequences WHERE table_name = 'requests') WHERE rowid = NEW.rowid;
END;

-- Version 11 also adds the descending completion-time access path used by the
-- unfiltered request-history listing. Keep the key-prefixed index from
-- version 10 for filtered traversals.
CREATE INDEX idx_requests_finished_desc
    ON requests(finished_at DESC, request_id DESC);
