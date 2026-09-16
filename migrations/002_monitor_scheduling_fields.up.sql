-- 002_monitor_scheduling_fields.up.sql
-- Add interval_ms, timeout_ms, and enabled to monitors table

ALTER TABLE monitors
    ADD COLUMN interval_ms BIGINT NOT NULL DEFAULT 60000 CHECK (interval_ms > 0),
    ADD COLUMN timeout_ms BIGINT NOT NULL DEFAULT 5000 CHECK (timeout_ms > 0),
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT true;
