import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import AccountEvaluationModal from '../AccountEvaluationModal.vue'
import type { AccountEvaluationTarget } from '@/api/admin/accountEvaluationHistory'

const { evaluate, models, getSettings, updateSettings } = vi.hoisted(() => ({ evaluate: vi.fn(), models: vi.fn(), getSettings: vi.fn(), updateSettings: vi.fn() }))
vi.mock('@/api/admin/accountEvaluationSettings', () => ({ getAccountEvaluationSettings: getSettings, updateAccountEvaluationSettings: updateSettings }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getAvailableModels: models } } }))
vi.mock('@/api/admin/accountEvaluation', async () => ({
  ...await vi.importActual<typeof import('@/api/admin/accountEvaluation')>('@/api/admin/accountEvaluation'),
  evaluateAccount: evaluate
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const account = (id: number): AccountEvaluationTarget => ({
  id, name: `Account ${id}`, platform: 'openai', type: 'oauth', schedulable: true,
  proxy_id: null, proxy_name: null, results: []
})
const stubs = {
  BaseDialog: defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' }),
  EvaluationSvgPreview: true
}

describe('AccountEvaluationModal', () => {
  beforeEach(() => {
    let settings = { enabled: false, interval_hours: 12, concurrency: 3, model_id: 'gpt-6.1-sol', effort: 'low' }
    getSettings.mockReset().mockImplementation(async () => ({ ...settings }))
    updateSettings.mockReset().mockImplementation(async updated => { settings = { ...updated }; return { ...settings } })
    evaluate.mockReset()
    models.mockReset().mockResolvedValue([{ id: 'custom-model', display_name: 'Custom model' }])
  })
  afterEach(() => vi.useRealTimers())

  it('loads the shared server options and saves the dropdown choice across reopen and remount', async () => {
    evaluate.mockResolvedValue(undefined)
    const props = { show: true, accounts: [account(1)] }
    const wrapper = mount(AccountEvaluationModal, { props, global: { stubs } })
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluation-model"]').element.tagName).toBe('SELECT')
    expect((wrapper.get('[data-testid="evaluation-model"]').element as HTMLSelectElement).value).toBe('gpt-6.1-sol')
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    expect(evaluate).toHaveBeenLastCalledWith(1, expect.objectContaining({ model_id: 'gpt-6.1-sol' }), expect.any(Function), expect.any(AbortSignal))
    await wrapper.get('[data-testid="evaluation-model"]').setValue('custom-model')
    await wrapper.get('[data-testid="evaluation-effort"]').setValue('high')
    await wrapper.get('[data-testid="evaluation-save-options"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({ model_id: 'custom-model', effort: 'high', enabled: false }), expect.any(AbortSignal))
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect((wrapper.get('[data-testid="evaluation-model"]').element as HTMLSelectElement).value).toBe('custom-model')
    wrapper.unmount()
    const reopened = mount(AccountEvaluationModal, { props, global: { stubs } })
    await flushPromises()
    expect((reopened.get('[data-testid="evaluation-model"]').element as HTMLSelectElement).value).toBe('custom-model')
    expect((reopened.get('[data-testid="evaluation-effort"]').element as HTMLSelectElement).value).toBe('high')
    reopened.unmount()
  })

  it('only runs on demand, supports pelican, and renders results without executing markup', async () => {
    evaluate.mockImplementation(async (_id, request, onEvent) => {
      onEvent({ type: 'evaluation_result', data: {
        id: 'pelican-1', kind: request.kind, model: 'gpt-5.4', effort: 'low', status: 'completed',
        answer: '<svg onload="alert(1)"></svg>', svg: '<svg></svg>', duration_ms: 1500,
        tested_at: '2026-11-01T00:00:00Z', run: 1, runs: 1
      } })
    })
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [account(1)] }, global: { stubs } })
    await flushPromises()
    expect(evaluate).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="evaluation-kind"]').setValue('pelican')
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    expect(evaluate).toHaveBeenCalledWith(1, expect.objectContaining({ kind: 'pelican', runs: 1 }), expect.any(Function), expect.any(AbortSignal))
    expect(wrapper.findComponent({ name: 'EvaluationSvgPreview' }).exists()).toBe(true)
    expect(wrapper.find('svg[onload]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.evaluation.status.completed')
    wrapper.unmount()
  })

  it('bounds batch concurrency and cancels all active requests without starting queued accounts', async () => {
    evaluate.mockImplementation((_id, _request, _onEvent, signal: AbortSignal) => new Promise((_, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [1, 2, 3, 4, 5].map(account) }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="evaluation-stop"]').trigger('click')
    await flushPromises()
    expect(evaluate).toHaveBeenCalledTimes(3)
    expect(evaluate.mock.calls.every(call => call[3].aborted)).toBe(true)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows only the last five records and distinguishes automatic stops and verified proxies', async () => {
    const row = account(1)
    row.results = Array.from({ length: 7 }, (_, index) => ({
      id: `history-${index}`, kind: 'candy', model: 'gpt-5.4', effort: 'low', status: 'wrong',
      answer: `answer-${index}`, duration_ms: 100, tested_at: '2026-11-01T00:00:00Z', run: 1, runs: 1,
      source: 'automatic', scheduling_stopped: index === 0, proxy_id: 7, proxy_name: 'Account proxy'
    }))
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [row] }, global: { stubs } })
    await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(5)
    expect(wrapper.text()).not.toContain('answer-5')
    expect(wrapper.text()).toContain('admin.accounts.evaluation.schedulingStoppedByTest')
    expect(wrapper.text()).toContain('admin.accounts.evaluation.source.automatic')
    expect(wrapper.text()).toContain('Account proxy')
    wrapper.unmount()
  })

  it('keeps the dialog usable when one account has no readable history or has disappeared', async () => {
    evaluate.mockResolvedValue(undefined)
    const missing = { ...account(1), available: false, history_error: 'missing_account' as const }
    const unreadable = { ...account(2), history_error: 'invalid_history' as const }
    const wrapper = mount(AccountEvaluationModal, {
      props: { show: true, accounts: [missing, unreadable] }, global: { stubs }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.evaluation.historyAccountUnavailable')
    expect(wrapper.text()).toContain('admin.accounts.evaluation.historyUnavailable')
    expect(models).toHaveBeenLastCalledWith(2)
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    expect(evaluate).toHaveBeenCalledTimes(1)
    expect(evaluate.mock.calls[0][0]).toBe(2)
    wrapper.unmount()
  })

  it('rejects invalid repeat counts and distinguishes request failures from wrong answers', async () => {
    evaluate.mockRejectedValue(new Error('Timed out'))
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [account(1)] }, global: { stubs } })
    await wrapper.get('[data-testid="evaluation-runs"]').setValue('0')
    expect(wrapper.get('[data-testid="evaluation-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="evaluation-runs"]').setValue('1')
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Timed out')
    expect(wrapper.text()).not.toContain('admin.accounts.evaluation.status.wrong')
    wrapper.unmount()
  })

  it('coalesces streaming chunks and bounds the visible tail before the next paint', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    let send!: (event: { type: string; text: string }) => void
    evaluate.mockImplementation((_id, _request, onEvent, signal: AbortSignal) => new Promise((_, reject) => {
      send = onEvent
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [account(1)] }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    for (let i = 0; i < 100; i++) send({ type: 'content', text: 'chunk' })
    await nextTick()
    expect(wrapper.find('pre').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(100)
    expect(wrapper.get('pre').text()).toBe('chunk'.repeat(100))
    send({ type: 'content', text: 'z'.repeat(17000) })
    await vi.advanceTimersByTimeAsync(100)
    expect(wrapper.get('pre').text()).toBe('z'.repeat(16000))
    await wrapper.get('[data-testid="evaluation-stop"]').trigger('click')
    await flushPromises()
    wrapper.unmount()
  })

  it('includes accounts with scheduling stopped in a manual batch', async () => {
    evaluate.mockResolvedValue(undefined)
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [{ ...account(1), schedulable: false }, account(2)] }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    await flushPromises()
    expect(evaluate.mock.calls.map(call => call[0])).toEqual([1, 2])
    wrapper.unmount()
  })

  it('does not start after the dialog closes while shared options are saving', async () => {
    let resolveSave!: (value: unknown) => void
    updateSettings.mockImplementation(settings => new Promise(resolve => { resolveSave = () => resolve(settings) }))
    const wrapper = mount(AccountEvaluationModal, { props: { show: true, accounts: [account(1)] }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-testid="evaluation-model"]').setValue('custom-model')
    await wrapper.get('[data-testid="evaluation-start"]').trigger('click')
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="evaluation-kind"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="evaluation-runs"]').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ show: false })
    resolveSave(undefined)
    await flushPromises()
    expect(evaluate).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
