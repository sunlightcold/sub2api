-- Add nullable group-level endpoint disable switches.
-- NULL and false both preserve existing behavior; only true blocks the endpoint.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS disable_responses_api BOOLEAN;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS disable_chat_completions_api BOOLEAN;

COMMENT ON COLUMN groups.disable_responses_api IS 'Whether this group disables Responses API. NULL/false keeps existing behavior.';
COMMENT ON COLUMN groups.disable_chat_completions_api IS 'Whether this group disables Chat Completions API. NULL/false keeps existing behavior.';
