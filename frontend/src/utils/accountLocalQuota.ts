import type { Account } from '@/types'
import type { AccountLocalQuotaSnapshot } from '@/types/accountLocalQuota'

const amountFields = [
  'quota_limit', 'quota_used', 'quota_5h_limit', 'quota_5h_used',
  'quota_daily_limit', 'quota_daily_used', 'quota_weekly_limit', 'quota_weekly_used'
] as const

export function mergeAccountLocalQuota<T extends Pick<Account, 'id' | 'updated_at'>>(
  account: T,
  snapshot?: AccountLocalQuotaSnapshot | null,
  previousVersion?: string
): T {
  if (!snapshot || snapshot.account_id !== account.id) return account
  const version = Date.parse(snapshot.updated_at)
  if (!Number.isFinite(version)) return account
  if (version < Date.parse(account.updated_at) || (previousVersion && version < Date.parse(previousVersion))) {
    return account
  }
  // Whitelist local fields: upstream windows, credentials and scheduling state
  // belong to separate response contracts and must not be replaced here.
  const patch: Partial<Pick<Account, typeof amountFields[number] | 'quota_5h_reset_at'>> = {}
  for (const field of amountFields) {
    const value = snapshot[field]
    if (typeof value === 'number' && Number.isFinite(value)) patch[field] = value
  }
  if (typeof snapshot.quota_5h_reset_at === 'string') patch.quota_5h_reset_at = snapshot.quota_5h_reset_at
  return Object.keys(patch).length ? { ...account, ...patch } : account
}
