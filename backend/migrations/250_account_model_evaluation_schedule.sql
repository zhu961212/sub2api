-- Internal job bookkeeping stays outside account JSONB, so bounded due scans
-- and lease renewals never deserialize or rewrite diagnostic answer histories.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS candy_evaluation_next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS model_evaluation_lease_token VARCHAR(64) NULL,
    ADD COLUMN IF NOT EXISTS model_evaluation_lease_until TIMESTAMPTZ NULL,
    ADD COLUMN IF NOT EXISTS model_evaluation_lease_automatic BOOLEAN NOT NULL DEFAULT FALSE;

-- Existing installations may have longer manual histories. Keep newest-first
-- ordering and preserve every other extra key, including all quota counters.
UPDATE accounts
SET extra = jsonb_set(extra, '{model_evaluation_results}', (
    SELECT COALESCE(jsonb_agg(item.value ORDER BY item.ordinality), '[]'::jsonb)
    FROM jsonb_array_elements(extra->'model_evaluation_results') WITH ORDINALITY item(value, ordinality)
    WHERE item.ordinality <= 5
))
WHERE CASE WHEN jsonb_typeof(extra->'model_evaluation_results') = 'array'
      THEN jsonb_array_length(extra->'model_evaluation_results') ELSE 0 END > 5;
