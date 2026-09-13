-- Version 10 adds the composite access path used when request history is
-- filtered by key and completion time.
CREATE INDEX idx_requests_key_finished
    ON requests(api_key_id, finished_at DESC, request_id DESC);
