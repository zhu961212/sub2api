import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import type { Account } from '@/types'
import AccountCapacityCell from '../AccountCapacityCell.vue'
import QuotaBadge from '../QuotaBadge.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function account(overrides: Partial<Account> = {}): Account {
  return { id: 1, platform: 'openai', type: 'oauth', concurrency: 5, current_concurrency: 2,
    quota_daily_limit: 30, quota_daily_used: 6, quota_5h_limit: 10, quota_5h_used: 4, ...overrides } as Account
}

describe('capacity column local spending protection', () => {
  it('shows both local five-hour and daily dollar budgets in capacity', () => {
    const wrapper = mount(AccountCapacityCell, { props: { account: account() }, global: { stubs: { CapacityBadge: true } } })
    const badges = wrapper.findAllComponents(QuotaBadge)
    expect(badges.map(badge => [badge.props('label'), badge.props('used'), badge.props('limit')])).toEqual([
      ['5h', 4, 10], ['D', 6, 30],
    ])
    expect(wrapper.text()).toContain('$4.00')
    expect(wrapper.text()).toContain('$30.00')
  })

  it.each([
    { quota_daily_limit: 0, quota_5h_limit: 0 },
    { parent_account_id: 99 },
  ])('does not duplicate unlimited or parent-owned budgets: %j', (overrides) => {
    const wrapper = mount(AccountCapacityCell, { props: { account: account(overrides) }, global: { stubs: { CapacityBadge: true } } })
    expect(wrapper.findAllComponents(QuotaBadge)).toHaveLength(0)
  })
})
