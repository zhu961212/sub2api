-- Diagnostics must remain indexed after account scheduling is stopped.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_accounts_candy_evaluation_all_due
    ON accounts (candy_evaluation_next_run_at, id)
    WHERE deleted_at IS NULL
      AND platform = 'openai'
      AND type = 'oauth'
      AND parent_account_id IS NULL
      AND status = 'active';

DROP INDEX CONCURRENTLY IF EXISTS idx_accounts_candy_evaluation_due;
