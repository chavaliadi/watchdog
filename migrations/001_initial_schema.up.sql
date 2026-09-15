-- 001_initial_schema.up.sql
-- Initial schema for Deployment Watchdog

CREATE TABLE monitors (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    target TEXT NOT NULL,
    method TEXT NULL,
    expected_status_range TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE monitor_states (
    monitor_id UUID PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE check_results (
    id BIGSERIAL PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    ok BOOLEAN NOT NULL,
    status_code INTEGER NULL,
    latency_ms BIGINT NOT NULL,
    error_class TEXT NULL,
    error_detail TEXT NULL,
    attempt_count INTEGER NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL,
    CHECK (latency_ms >= 0),
    CHECK (attempt_count > 0)
);

CREATE INDEX idx_check_results_monitor_checked_at ON check_results (monitor_id, checked_at DESC);
