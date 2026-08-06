-- One-time data repair for OpenAI usage logs created on 2026-08-06
-- (Asia/Shanghai). This is intentionally a manual script, not an automatic
-- application migration: the date window is explicit and must be reviewed
-- before execution.
--
-- What it changes:
--   * folds cache-creation token columns into input_tokens;
--   * folds cache_creation_cost into input_cost;
--   * clears cache-creation token/cost columns.
--
-- OpenAI usage only uses cache_creation_tokens. The cache_creation_5m_tokens
-- and cache_creation_1h_tokens columns belong to the Anthropic-compatible
-- pipeline and are intentionally not touched here.
--
-- What it does NOT change:
--   * total_cost, actual_cost, account_stats_cost, or any balance/quota data.
--
-- The candidate predicate makes this script idempotent. Re-running it after a
-- successful execution produces zero candidates.
--
-- Usage:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 \
--     -f backend/scripts/fix-openai-cache-creation-2026-08-06.sql

BEGIN;

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TEMP TABLE openai_cache_creation_repair_candidates (
    id BIGINT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO openai_cache_creation_repair_candidates (id)
SELECT ul.id
FROM usage_logs AS ul
JOIN accounts AS a ON a.id = ul.account_id
WHERE a.platform = 'openai'
  -- Explicit Asia/Shanghai date window: 2026-08-06 00:00:00 through
  -- 2026-08-07 00:00:00. Change both literals after reviewing the target date.
  AND ul.created_at >= TIMESTAMPTZ '2026-08-06 00:00:00+08'
  AND ul.created_at <  TIMESTAMPTZ '2026-08-07 00:00:00+08'
  AND (
      ul.cache_creation_tokens <> 0
      OR ul.cache_creation_cost <> 0
  );

CREATE TEMP TABLE openai_cache_creation_repair_totals ON COMMIT DROP AS
SELECT
    COUNT(*)::BIGINT AS row_count,
    COALESCE(SUM(ul.input_tokens::BIGINT + ul.cache_creation_tokens::BIGINT), 0)::NUMERIC
        AS folded_input_tokens_before,
    COALESCE(SUM(ul.input_cost + ul.cache_creation_cost), 0)::NUMERIC
        AS folded_input_cost_before,
    COALESCE(SUM(ul.total_cost), 0)::NUMERIC AS total_cost_before,
    COALESCE(SUM(ul.actual_cost), 0)::NUMERIC AS actual_cost_before
FROM usage_logs AS ul
JOIN openai_cache_creation_repair_candidates AS c ON c.id = ul.id;

-- Informational candidate summary; the UPDATE below runs in the same transaction.
SELECT
    COUNT(*) AS rows_to_update,
    COALESCE(SUM(ul.input_tokens), 0) AS input_tokens_before,
    COALESCE(SUM(ul.cache_creation_tokens), 0) AS cache_creation_tokens_to_fold,
    COALESCE(SUM(ul.input_cost), 0) AS input_cost_before,
    COALESCE(SUM(ul.cache_creation_cost), 0) AS cache_creation_cost_to_fold,
    COALESCE(SUM(ul.total_cost), 0) AS total_cost_before,
    COALESCE(SUM(ul.actual_cost), 0) AS actual_cost_before
FROM usage_logs AS ul
JOIN openai_cache_creation_repair_candidates AS c ON c.id = ul.id;

UPDATE usage_logs AS ul
SET input_tokens = ul.input_tokens
        + ul.cache_creation_tokens,
    input_cost = ul.input_cost + ul.cache_creation_cost,
    cache_creation_tokens = 0,
    cache_creation_cost = 0
FROM openai_cache_creation_repair_candidates AS c
WHERE c.id = ul.id;

-- Post-update invariants: no cache-creation fields remain in the candidate set,
-- and the already-settled totals are byte-for-byte unchanged at SQL precision.
SELECT
    COUNT(*) FILTER (WHERE ul.cache_creation_tokens <> 0 OR ul.cache_creation_cost <> 0)
        AS rows_with_openai_cache_creation_remaining,
    COALESCE(SUM(ul.total_cost), 0) AS total_cost_after,
    COALESCE(SUM(ul.actual_cost), 0) AS actual_cost_after
FROM usage_logs AS ul
JOIN openai_cache_creation_repair_candidates AS c ON c.id = ul.id;

DO $$
DECLARE
    before_rows BIGINT;
    before_input_tokens NUMERIC;
    before_input_cost NUMERIC;
    before_total NUMERIC;
    before_actual NUMERIC;
    after_rows BIGINT;
    after_input_tokens NUMERIC;
    after_input_cost NUMERIC;
    after_total NUMERIC;
    after_actual NUMERIC;
    remaining_rows BIGINT;
BEGIN
    SELECT
        row_count,
        folded_input_tokens_before,
        folded_input_cost_before,
        total_cost_before,
        actual_cost_before
    INTO
        before_rows,
        before_input_tokens,
        before_input_cost,
        before_total,
        before_actual
    FROM openai_cache_creation_repair_totals;

    SELECT
        COUNT(*),
        COALESCE(SUM(ul.input_tokens::BIGINT), 0),
        COALESCE(SUM(ul.input_cost), 0),
        COALESCE(SUM(ul.total_cost), 0),
        COALESCE(SUM(ul.actual_cost), 0),
        COUNT(*) FILTER (WHERE ul.cache_creation_tokens <> 0 OR ul.cache_creation_cost <> 0)
    INTO
        after_rows,
        after_input_tokens,
        after_input_cost,
        after_total,
        after_actual,
        remaining_rows
    FROM usage_logs AS ul
    JOIN openai_cache_creation_repair_candidates AS c ON c.id = ul.id;

    IF before_rows IS DISTINCT FROM after_rows
       OR before_input_tokens IS DISTINCT FROM after_input_tokens
       OR before_input_cost IS DISTINCT FROM after_input_cost
       OR before_total IS DISTINCT FROM after_total
       OR before_actual IS DISTINCT FROM after_actual THEN
        RAISE EXCEPTION
            'OpenAI cache repair invariant failed: rows % -> %, input tokens % -> %, input cost % -> %, total % -> %, actual % -> %',
            before_rows, after_rows,
            before_input_tokens, after_input_tokens,
            before_input_cost, after_input_cost,
            before_total, after_total,
            before_actual, after_actual;
    END IF;

    IF remaining_rows <> 0 THEN
        RAISE EXCEPTION
            'OpenAI cache repair left % candidate rows with cache-creation data',
            remaining_rows;
    END IF;
END;
$$;

COMMIT;
