export interface AccountLocalQuotaSnapshot {
  account_id: number
  updated_at: string
  quota_limit?: number
  quota_used?: number
  quota_5h_limit?: number
  quota_5h_used?: number
  quota_5h_reset_at?: string
  quota_daily_limit?: number
  quota_daily_used?: number
  quota_weekly_limit?: number
  quota_weekly_used?: number
}
