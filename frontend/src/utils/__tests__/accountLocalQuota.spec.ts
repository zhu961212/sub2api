import { describe, expect, it } from 'vitest'
import { mergeAccountLocalQuota } from '../accountLocalQuota'

const account = {
  id: 42,
  updated_at: '2026-11-03T01:00:00Z',
  quota_5h_used: 1,
  quota_daily_used: 3,
  quota_weekly_used: 9,
  quota_used: 20,
  schedulable: false,
  extra: { codex_5h_used_percent: 60 }
}
const snapshot = {
  account_id: 42,
  updated_at: '2026-11-03T01:01:00Z',
  quota_daily_used: 4
}

describe('mergeAccountLocalQuota', () => {
  it('merges local spending without changing upstream windows or scheduling', () => {
    const updated = mergeAccountLocalQuota(account, snapshot)
    expect(updated.quota_daily_used).toBe(4)
    expect(updated.quota_5h_used).toBe(1)
    expect(updated.extra).toBe(account.extra)
    expect(updated.schedulable).toBe(false)
  })

  it('preserves all counters when the server sends no local snapshot', () => {
    expect(mergeAccountLocalQuota(account)).toBe(account)
    expect(mergeAccountLocalQuota(account, null)).toBe(account)
  })

  it('does not turn omitted counters into zero or undefined', () => {
    const updated = mergeAccountLocalQuota(account, { ...snapshot, quota_5h_used: 2 })
    expect(updated.quota_weekly_used).toBe(9)
    expect(updated.quota_used).toBe(20)
  })

  it('accepts an explicit zero after a local window resets', () => {
    expect(mergeAccountLocalQuota(account, { ...snapshot, quota_daily_used: 0 }).quota_daily_used).toBe(0)
  })

  it('ignores snapshots for another account', () => {
    expect(mergeAccountLocalQuota(account, { ...snapshot, account_id: 99 })).toBe(account)
  })

  it('ignores a snapshot older than the list row or a previous response', () => {
    expect(mergeAccountLocalQuota(account, { ...snapshot, updated_at: '2026-11-03T00:59:00Z' })).toBe(account)
    expect(mergeAccountLocalQuota(account, snapshot, '2026-11-03T01:02:00Z')).toBe(account)
  })

  it('ignores malformed versions and non-finite amounts', () => {
    expect(mergeAccountLocalQuota(account, { ...snapshot, updated_at: 'invalid' })).toBe(account)
    expect(mergeAccountLocalQuota(account, { ...snapshot, quota_daily_used: NaN })).toBe(account)
  })

  it('does not spread unrelated server properties into the account', () => {
    const response = { ...snapshot, schedulable: true, credentials: { access_token: 'unexpected' } }
    const updated = mergeAccountLocalQuota(account, response)
    expect(updated.schedulable).toBe(false)
    expect(updated).not.toHaveProperty('credentials')
  })
})
