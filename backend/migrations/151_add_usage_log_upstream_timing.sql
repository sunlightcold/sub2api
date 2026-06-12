-- Store administrator-only per-request upstream timing diagnostics.
-- Values are raw milliseconds and are not adjusted by usage latency offsets.

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS upstream_timing JSONB;
