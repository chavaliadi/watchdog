-- 001_initial_schema.down.sql
-- Revert initial schema for Deployment Watchdog

DROP INDEX IF EXISTS idx_check_results_monitor_checked_at;
DROP TABLE IF EXISTS check_results;
DROP TABLE IF EXISTS monitor_states;
DROP TABLE IF EXISTS monitors;
