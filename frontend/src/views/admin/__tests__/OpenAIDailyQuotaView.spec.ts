import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIDailyQuotaView from '../OpenAIDailyQuotaView.vue'
import type { OpenAIDailyQuotaPolicy, OpenAIDailyQuotaResponse } from '@/api/admin/openaiDailyQuota'

enableAutoUnmount(afterEach)

const { getPolicy, updatePolicy, showSuccess, showError } = vi.hoisted(() => ({
  getPolicy: vi.fn(),
  updatePolicy: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin/openaiDailyQuota', () => ({
  getOpenAIDailyQuota: getPolicy,
  updateOpenAIDailyQuota: updatePolicy,
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key}${params ? ` ${JSON.stringify(params)}` : ''}` }),
}))

function response(policy?: OpenAIDailyQuotaPolicy): OpenAIDailyQuotaResponse {
  return {
    policy: policy ?? {
      enabled: true,
      reset_hour: 0,
      reset_timezone: 'Asia/Shanghai',
      plan_limits: { plus: 30, pro: 400, pro_100: 400, pro_200: 400, pro_500: 400, free: 0, unknown: 0, retired_plan: 7 },
      plan_5h_limits: { plus: 5, pro: 25, free: 0, retired_plan: 2 },
    },
    plans: [
      { plan_type: 'free', account_count: 4 },
      { plan_type: 'plus', account_count: 3 },
      { plan_type: 'pro', account_count: 2 },
      { plan_type: 'pro_100', account_count: 0 },
      { plan_type: 'pro_200', account_count: 0 },
      { plan_type: 'pro_500', account_count: 0 },
      { plan_type: 'unknown', account_count: 0 },
    ],
    account_count: 9,
    applied_count: 9,
  }
}

describe('OpenAI daily quota policy', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getPolicy.mockResolvedValue(response())
    updatePolicy.mockImplementation(async (policy: OpenAIDailyQuotaPolicy) => response(policy))
  })

  it('shows only plans with existing OAuth accounts, keeping configured and empty tiers hidden', async () => {
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    expect(wrapper.get('[data-testid="account-count"]').text()).toContain('"count":9')
    expect(wrapper.get('[data-testid="plan-row-plus"]').text()).toContain('3')
    expect(wrapper.find('[data-testid="default-limit"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="plan-effective-plus"]').text()).toContain('"amount":30')
    expect(wrapper.get('[data-testid="plan-5h-limit-plus"]').element).toHaveProperty('value', '5')
    expect(wrapper.get('[data-testid="plan-5h-effective-plus"]').text()).toContain('"amount":5')
    expect(wrapper.get<HTMLInputElement>('[data-testid="plan-limit-pro"]').element.value).toBe('400')
    for (const plan of ['pro_100', 'pro_200', 'pro_500', 'unknown', 'retired_plan']) {
      expect(wrapper.find(`[data-testid="plan-row-${plan}"]`).exists()).toBe(false)
    }
    expect(wrapper.get('[data-testid="plan-row-pro"]').text()).toContain('Pro 200')
    expect(wrapper.get('[data-testid="plan-effective-free"]').text()).toContain('unlimited')
    expect(wrapper.get('[data-testid="reset-summary"]').text()).toContain('Asia/Shanghai')
  })

  it('saves visible plan amounts, including zero, while preserving hidden plan limits', async () => {
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    await wrapper.get('[data-testid="plan-limit-plus"]').setValue('45')
    await wrapper.get('[data-testid="plan-limit-pro"]').setValue('0')
    await wrapper.get('[data-testid="reset-timezone"]').setValue('UTC')
    await wrapper.get('[data-testid="reset-hour"]').setValue('8')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).toHaveBeenCalledOnce()
    expect(updatePolicy).toHaveBeenCalledWith({
      enabled: true,
      reset_hour: 8,
      reset_timezone: 'UTC',
      plan_limits: { plus: 45, pro: 0, pro_100: 400, pro_200: 400, pro_500: 400, free: 0, unknown: 0, retired_plan: 7 },
      plan_5h_limits: { plus: 5, pro: 25, free: 0, retired_plan: 2 },
    })
    expect(updatePolicy.mock.calls[0][0]).not.toHaveProperty('default_daily_limit')
    expect(wrapper.get('[data-testid="save-result"]').text()).toContain('"count":9')
    expect(showSuccess).toHaveBeenCalledOnce()
  })

  it.each([
    ['plan-limit-plus', '-5', 'invalidPlan'],
    ['plan-limit-plus', '', 'invalidPlan'],
    ['plan-limit-pro', '1000000000001', 'invalidPlan'],
    ['plan-5h-limit-plus', '-5', 'invalid5hPlan'],
    ['plan-5h-limit-plus', '', 'invalid5hPlan'],
    ['plan-5h-limit-pro', '1000000000001', 'invalid5hPlan'],
    ['reset-timezone', 'Not/A_Timezone', 'invalidTimezone'],
  ])('rejects invalid %s value %s without sending a policy', async (field, value, errorKey) => {
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    await wrapper.get(`[data-testid="${field}"]`).setValue(value)
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="save-error"]').text()).toContain(errorKey)
  })

  it('shows a retryable load error instead of allowing an empty policy to overwrite settings', async () => {
    getPolicy.mockRejectedValueOnce(new Error('network unavailable'))
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('loadError')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="plan-limit-plus"]').element).toHaveProperty('value', '30')
  })

  it('saves five-hour limits independently and preserves hidden plan overrides', async () => {
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    await wrapper.get('[data-testid="plan-5h-limit-plus"]').setValue('3.5')
    await wrapper.get('[data-testid="plan-5h-limit-pro"]').setValue('0')
    expect(wrapper.get('[data-testid="plan-5h-effective-pro"]').text()).toContain('unlimited')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      plan_limits: response().policy.plan_limits,
      plan_5h_limits: { plus: 3.5, pro: 0, free: 0, retired_plan: 2 },
    }))
  })

  it('keeps five-hour quotas unlimited when reading an older policy', async () => {
    const state = response()
    delete state.policy.plan_5h_limits
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    expect(wrapper.get('[data-testid="plan-5h-limit-plus"]').element).toHaveProperty('value', '0')
    expect(wrapper.get('[data-testid="plan-5h-effective-plus"]').text()).toContain('unlimited')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({ plan_5h_limits: { free: 0, plus: 0, pro: 0 } }))
  })

  it('disables editing during save, then preserves the draft when saving fails', async () => {
    let rejectSave: (reason: Error) => void = () => undefined
    updatePolicy.mockReturnValueOnce(new Promise((_resolve, reject) => { rejectSave = reject }))
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    await wrapper.get('[data-testid="plan-limit-plus"]').setValue('42')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('fieldset').attributes()).toHaveProperty('disabled')
    expect(wrapper.get('[data-testid="save-policy"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('form').trigger('submit')
    expect(updatePolicy).toHaveBeenCalledOnce()

    rejectSave(new Error('server could not apply policy'))
    await flushPromises()
    expect(wrapper.get('[data-testid="plan-limit-plus"]').element).toHaveProperty('value', '42')
    expect(wrapper.get('fieldset').attributes()).not.toHaveProperty('disabled')
    expect(wrapper.get('[data-testid="save-error"]').text()).toContain('server could not apply policy')
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('preserves the configured plan amounts when disabling the policy', async () => {
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()
    await wrapper.get('[data-testid="policy-enabled"]').setValue(false)
    expect(wrapper.get('[data-testid="plan-effective-free"]').text()).toContain('.off')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      enabled: false,
      plan_limits: { plus: 30, pro: 400, pro_100: 400, pro_200: 400, pro_500: 400, free: 0, unknown: 0, retired_plan: 7 },
    }))
  })

  it('uses server-provided amounts and initializes an unconfigured discovered plan to zero', async () => {
    const state = response()
    state.policy.plan_limits.plus = 72
    state.plans.push({ plan_type: 'new_plan', account_count: 1 })
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    expect(wrapper.get('[data-testid="plan-limit-plus"]').element).toHaveProperty('value', '72')
    expect(wrapper.get('[data-testid="plan-limit-new_plan"]').element).toHaveProperty('value', '0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      plan_limits: expect.objectContaining({ plus: 72, new_plan: 0 }),
    }))
  })

  it('shows the empty state when no OAuth plan has accounts and preserves saved defaults', async () => {
    const state = response()
    state.plans = state.plans.map(plan => ({ ...plan, account_count: 0 }))
    state.account_count = 0
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    expect(wrapper.findAll('[data-testid^="plan-row-"]')).toHaveLength(0)
    expect(wrapper.get('tbody').text()).toContain('emptyPlans')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updatePolicy).toHaveBeenCalledWith(state.policy)
  })

  it('shows a Pro tier once OAuth accounts for that tier exist', async () => {
    const state = response()
    state.plans.find(plan => plan.plan_type === 'pro_100')!.account_count = 1
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    expect(wrapper.get('[data-testid="plan-row-pro_100"]').text()).toContain('Pro 100')
    expect(wrapper.get('[data-testid="plan-limit-pro_100"]').element).toHaveProperty('value', '400')
  })

  it('displays recognized raw SKUs with effective amounts and saves their own overrides independently', async () => {
    const state = response()
    Object.assign(state.policy.plan_limits, { prolite: 400, chatgptpro: 0, promax: 400 })
    state.plans.push(
      { plan_type: 'prolite', account_count: 1 },
      { plan_type: 'chatgptpro', account_count: 1 },
      { plan_type: 'promax', account_count: 1 },
    )
    state.account_count = 12
    getPolicy.mockResolvedValueOnce(state)
    updatePolicy.mockImplementationOnce(async (policy: OpenAIDailyQuotaPolicy) => ({ ...state, policy }))
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    for (const [sku, label, amount] of [
      ['prolite', 'Pro 100', '400'], ['chatgptpro', 'Pro 200', '0'], ['promax', 'Pro 500', '400'],
    ]) {
      expect(wrapper.get(`[data-testid="plan-row-${sku}"]`).text()).toContain(label)
      expect(wrapper.get(`[data-testid="plan-limit-${sku}"]`).element).toHaveProperty('value', amount)
    }
    expect(wrapper.get('[data-testid="plan-effective-prolite"]').text()).toContain('"amount":400')
    expect(wrapper.get('[data-testid="plan-effective-chatgptpro"]').text()).toContain('unlimited')
    expect(wrapper.get('[data-testid="plan-row-pro"]').text()).toContain('Pro 200')

    await wrapper.get('[data-testid="plan-limit-prolite"]').setValue('0')
    await wrapper.get('[data-testid="plan-limit-promax"]').setValue('75')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      plan_limits: expect.objectContaining({
        prolite: 0, chatgptpro: 0, promax: 75,
        pro_100: 400, pro_200: 400, pro_500: 400, retired_plan: 7,
      }),
    }))
    expect(wrapper.get('[data-testid="plan-limit-prolite"]').element).toHaveProperty('value', '0')
    expect(wrapper.get('[data-testid="plan-effective-prolite"]').text()).toContain('unlimited')
    expect(wrapper.get('[data-testid="plan-effective-promax"]').text()).toContain('"amount":75')
  })

  it('labels native Pro as Pro 200 while preserving its explicit zero and other SKU overrides', async () => {
    const state = response()
    state.policy.plan_limits.pro = 0
    state.policy.plan_5h_limits = { pro: 0, pro_200: 25 }
    state.plans = [
      { plan_type: 'pro', account_count: 2 },
      { plan_type: 'pro_200', account_count: 1 },
      { plan_type: 'professional', account_count: 1 },
    ]
    state.policy.plan_limits.professional = 7
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    expect(wrapper.get('[data-testid="plan-row-pro"] th').text()).toBe('Pro 200')
    expect(wrapper.get('[data-testid="plan-row-pro_200"] th').text()).toBe('Pro 200')
    expect(wrapper.get('[data-testid="plan-row-professional"] th').text()).toBe('professional')
    expect(wrapper.get('[data-testid="plan-effective-pro"]').text()).toContain('unlimited')
    expect(wrapper.get('[data-testid="plan-5h-effective-pro"]').text()).toContain('unlimited')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      plan_limits: expect.objectContaining({ pro: 0, pro_200: 400, professional: 7 }),
      plan_5h_limits: expect.objectContaining({ pro: 0, pro_200: 25 }),
    }))
  })

  it('uses native plan labels for Pro 500 and legacy tiers without losing localized or unknown names', async () => {
    const state = response()
    state.plans = [
      { plan_type: 'promax', account_count: 1 },
      { plan_type: 'pro_100', account_count: 1 },
      { plan_type: 'pro_200', account_count: 1 },
      { plan_type: 'pro_500', account_count: 1 },
      { plan_type: 'unknown', account_count: 1 },
      { plan_type: 'custom_plan', account_count: 1 },
    ]
    Object.assign(state.policy.plan_limits, { promax: 75, unknown: 0, custom_plan: 12 })
    getPolicy.mockResolvedValueOnce(state)
    const wrapper = mount(OpenAIDailyQuotaView)
    await flushPromises()

    for (const [plan, label] of [
      ['promax', 'Pro 500'], ['pro_100', 'Pro 100'], ['pro_200', 'Pro 200'], ['pro_500', 'Pro 500'],
      ['unknown', 'admin.openaiDailyQuota.unknownPlan'], ['custom_plan', 'custom_plan'],
    ]) {
      expect(wrapper.get(`[data-testid="plan-row-${plan}"] th`).text()).toBe(label)
    }
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(updatePolicy).toHaveBeenCalledWith(expect.objectContaining({
      plan_limits: expect.objectContaining({ promax: 75, pro_100: 400, pro_200: 400, pro_500: 400, unknown: 0, custom_plan: 12 }),
    }))
  })
})
