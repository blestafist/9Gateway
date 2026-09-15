-- Version 12 records request metadata deletions so a cursor can fail closed
-- even when a row other than its bookmark was removed by retention.
ALTER TABLE traversal_sequences ADD COLUMN deletion_seq INTEGER NOT NULL DEFAULT 0;

CREATE TRIGGER requests_deletion_sequence
AFTER DELETE ON requests
BEGIN
    UPDATE traversal_sequences SET deletion_seq = deletion_seq + 1 WHERE table_name = 'requests';
END;
