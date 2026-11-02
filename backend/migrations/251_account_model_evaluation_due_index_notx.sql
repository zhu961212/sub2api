CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_candy_evaluation_due
    ON accounts (candy_evaluation_next_run_at, id)
    WHERE deleted_at IS NULL
      AND platform = 'openai'
      AND type = 'oauth'
      AND parent_account_id IS NULL
      AND status = 'active'
      AND schedulable = TRUE;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_model_evaluation_active_leases
    ON accounts (model_evaluation_lease_until)
    WHERE model_evaluation_lease_automatic = TRUE
      AND model_evaluation_lease_until IS NOT NULL;
