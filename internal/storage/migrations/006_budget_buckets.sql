-- Version 6 stores only committed budget aggregates. Reservations, leases, and
-- transport state remain process-local. The schema shape follows the gateway's
-- stable API-key identity rather than a raw credential.
--
-- Provenance review: Bifrost commit
-- 03ab391865710462302bbcf52dca2f32682b91b5, inspected paths
-- .references/bifrost/plugins/governance/store.go,
-- .references/bifrost/plugins/governance/budgetcycle_test.go, and
-- .references/bifrost/framework/configstore/tables/budget.go. Those sources
-- informed the boundary between durable aggregates and runtime budget state;
-- no Bifrost code or data is copied or adapted and no dependency is added.
CREATE TABLE budget_buckets (
    api_key_id TEXT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    period_kind TEXT NOT NULL
        CHECK (typeof(period_kind) = 'text' AND period_kind IN ('total', 'day', 'month')),
    period_start INTEGER NOT NULL
        CHECK (
            typeof(period_start) = 'integer'
            AND period_start >= 0
            AND period_start <= 253402300799
            AND (
                (period_kind = 'total' AND period_start = 0)
                OR (period_kind = 'day' AND period_start % 86400 = 0)
                OR (
                    period_kind = 'month'
                    AND strftime('%d', period_start, 'unixepoch') = '01'
                    AND strftime('%H:%M:%S', period_start, 'unixepoch') = '00:00:00'
                )
            )
        ),
    spent_micros INTEGER NOT NULL
        CHECK (typeof(spent_micros) = 'integer' AND spent_micros >= 0),
    created_at INTEGER NOT NULL
        CHECK (typeof(created_at) = 'integer' AND created_at >= 0 AND created_at <= 253402300799),
    updated_at INTEGER NOT NULL
        CHECK (
            typeof(updated_at) = 'integer'
            AND updated_at >= created_at
            AND updated_at <= 253402300799
        ),
    PRIMARY KEY (api_key_id, period_kind, period_start)
);

CREATE INDEX idx_budget_buckets_key_period
    ON budget_buckets(api_key_id, period_kind, period_start);
CREATE INDEX idx_budget_buckets_expiration
    ON budget_buckets(period_kind, period_start);
