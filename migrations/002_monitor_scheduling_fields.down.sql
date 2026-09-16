-- 002_monitor_scheduling_fields.down.sql
-- Revert monitor scheduling fields

ALTER TABLE monitors
    DROP COLUMN IF EXISTS enabled,
    DROP COLUMN IF EXISTS timeout_ms,
    DROP COLUMN IF EXISTS interval_ms;
