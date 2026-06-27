ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_billing_use_requested_count BOOLEAN;

COMMENT ON COLUMN groups.image_billing_use_requested_count IS 'Whether image generation billing uses requested n instead of upstream output count. NULL/false keeps legacy behavior.';
