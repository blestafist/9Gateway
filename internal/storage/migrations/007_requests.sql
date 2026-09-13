-- Version 7 stores bounded request history without retaining credentials or
-- payload data. A deleted API key leaves the historical row in place.
CREATE TABLE requests (
    request_id TEXT NOT NULL PRIMARY KEY
        CHECK (
            typeof(request_id) = 'text'
            AND length(request_id) = 32
            AND request_id NOT GLOB '*[^0-9a-f]*'
        ),
    api_key_id TEXT NULL REFERENCES api_keys(id) ON DELETE SET NULL
        CHECK (
            api_key_id IS NULL
            OR (typeof(api_key_id) = 'text' AND length(trim(api_key_id)) > 0 AND length(api_key_id) <= 256)
        ),
    key_name TEXT NULL
        CHECK (
            key_name IS NULL
            OR (
                typeof(key_name) = 'text'
                AND length(trim(key_name)) > 0
                AND length(key_name) <= 256
            )
        ),
    method TEXT NULL
        CHECK (
            method IS NULL
            OR (typeof(method) = 'text' AND length(method) <= 32)
        ),
    path TEXT NULL
        CHECK (
            path IS NULL
            OR (typeof(path) = 'text' AND length(path) <= 2048)
        ),
    route TEXT NULL
        CHECK (
            route IS NULL
            OR (
                typeof(route) = 'text'
                AND route IN ('models', 'chat_completions', 'responses', 'generic', 'health', 'admin')
            )
        ),
    model TEXT NULL
        CHECK (
            model IS NULL
            OR (typeof(model) = 'text' AND length(model) <= 512)
        ),
    requested_mode TEXT NULL
        CHECK (requested_mode IS NULL OR requested_mode IN ('json', 'sse')),
    upstream_mode TEXT NULL
        CHECK (upstream_mode IS NULL OR upstream_mode IN ('json', 'opaque', 'sse')),
    delivered_mode TEXT NULL
        CHECK (delivered_mode IS NULL OR delivered_mode IN ('json', 'opaque', 'sse')),
    downstream_status INTEGER NULL
        CHECK (
            downstream_status IS NULL
            OR (typeof(downstream_status) = 'integer' AND downstream_status >= 0 AND downstream_status <= 999)
        ),
    upstream_status INTEGER NULL
        CHECK (
            upstream_status IS NULL
            OR (typeof(upstream_status) = 'integer' AND upstream_status >= 0 AND upstream_status <= 999)
        ),
    terminal_outcome TEXT NULL
        CHECK (
            terminal_outcome IS NULL
            OR terminal_outcome IN ('pre_upstream', 'upstream_error', 'response_error', 'complete', 'custom_dispatch', 'cancelled')
        ),
    upstream_started INTEGER NOT NULL
        CHECK (typeof(upstream_started) = 'integer' AND upstream_started IN (0, 1)),
    error_code TEXT NULL
        CHECK (
            error_code IS NULL
            OR error_code IN (
                'invalid_api_key', 'key_disabled', 'key_expired', 'invalid_request',
                'model_not_allowed', 'request_limit_exceeded', 'concurrency_limit_exceeded',
                'token_limit_exceeded', 'budget_exceeded', 'upstream_connection_error',
                'upstream_timeout', 'response_transport_error', 'conversion_error',
                'cancelled', 'unsupported_response', 'gateway_internal_error',
                'not_found', 'conflict'
            )
        ),
    client_bytes INTEGER NULL
        CHECK (client_bytes IS NULL OR (typeof(client_bytes) = 'integer' AND client_bytes >= 0)),
    upstream_bytes INTEGER NULL
        CHECK (upstream_bytes IS NULL OR (typeof(upstream_bytes) = 'integer' AND upstream_bytes >= 0)),
    delivered_bytes INTEGER NULL
        CHECK (delivered_bytes IS NULL OR (typeof(delivered_bytes) = 'integer' AND delivered_bytes >= 0)),
    input_tokens INTEGER NULL
        CHECK (input_tokens IS NULL OR (typeof(input_tokens) = 'integer' AND input_tokens >= 0)),
    output_tokens INTEGER NULL
        CHECK (output_tokens IS NULL OR (typeof(output_tokens) = 'integer' AND output_tokens >= 0)),
    total_tokens INTEGER NULL
        CHECK (total_tokens IS NULL OR (typeof(total_tokens) = 'integer' AND total_tokens >= 0)),
    cached_input_tokens INTEGER NULL
        CHECK (cached_input_tokens IS NULL OR (typeof(cached_input_tokens) = 'integer' AND cached_input_tokens >= 0)),
    reasoning_output_tokens INTEGER NULL
        CHECK (reasoning_output_tokens IS NULL OR (typeof(reasoning_output_tokens) = 'integer' AND reasoning_output_tokens >= 0)),
    cost_micros INTEGER NULL
        CHECK (cost_micros IS NULL OR (typeof(cost_micros) = 'integer' AND cost_micros >= 0)),
    started_at INTEGER NULL
        CHECK (started_at IS NULL OR typeof(started_at) = 'integer'),
    upstream_started_at INTEGER NULL
        CHECK (upstream_started_at IS NULL OR typeof(upstream_started_at) = 'integer'),
    upstream_headers_at INTEGER NULL
        CHECK (upstream_headers_at IS NULL OR typeof(upstream_headers_at) = 'integer'),
    first_byte_at INTEGER NULL
        CHECK (first_byte_at IS NULL OR typeof(first_byte_at) = 'integer'),
    finished_at INTEGER NULL
        CHECK (finished_at IS NULL OR typeof(finished_at) = 'integer'),
    total_micros INTEGER NULL
        CHECK (total_micros IS NULL OR (typeof(total_micros) = 'integer' AND total_micros >= 0)),
    time_to_upstream_headers_micros INTEGER NULL
        CHECK (time_to_upstream_headers_micros IS NULL OR (typeof(time_to_upstream_headers_micros) = 'integer' AND time_to_upstream_headers_micros >= 0)),
    time_to_first_byte_micros INTEGER NULL
        CHECK (time_to_first_byte_micros IS NULL OR (typeof(time_to_first_byte_micros) = 'integer' AND time_to_first_byte_micros >= 0)),
    stream_close_delay_micros INTEGER NULL
        CHECK (stream_close_delay_micros IS NULL OR (typeof(stream_close_delay_micros) = 'integer' AND stream_close_delay_micros >= 0)),
    CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at),
    CHECK (
        upstream_mode IS NOT NULL
        OR delivered_mode IS NULL
        OR delivered_mode = 'opaque'
    ),
    CHECK (
        delivered_mode IS NULL
        OR delivered_mode <> 'sse'
        OR (upstream_mode IS NOT NULL AND upstream_mode = 'sse')
    ),
    CHECK (
        delivered_mode IS NULL
        OR delivered_mode <> 'opaque'
        OR upstream_mode IS NULL
        OR upstream_mode = 'opaque'
    ),
    CHECK (
        upstream_mode IS NULL
        OR upstream_mode <> 'opaque'
        OR delivered_mode IS NULL
        OR delivered_mode = 'opaque'
    ),
    CHECK (requested_mode IS NULL OR delivered_mode IS NULL OR NOT (requested_mode = 'json' AND delivered_mode = 'sse')),
    CHECK (requested_mode IS NULL OR delivered_mode IS NULL OR NOT (requested_mode = 'sse' AND delivered_mode = 'json')),
    CHECK (terminal_outcome IS NULL OR terminal_outcome <> 'pre_upstream' OR upstream_started = 0),
    CHECK (
        terminal_outcome IS NULL
        OR terminal_outcome IN ('unknown', 'pre_upstream')
        OR upstream_started = 1
    )
);

CREATE INDEX idx_requests_recent
    ON requests(started_at DESC, request_id);
CREATE INDEX idx_requests_key_time
    ON requests(api_key_id, started_at DESC, request_id);
