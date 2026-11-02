import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountEvaluationAutomationSettings from '../AccountEvaluationAutomationSettings.vue'
import AccountEvaluationOptions from '../AccountEvaluationOptions.vue'
import { defineComponent } from 'vue'

const { getSettings, updateSettings } = vi.hoisted(() => ({ getSettings: vi.fn(), updateSettings: vi.fn() }))
vi.mock('@/api/admin/accountEvaluationSettings', () => ({
  getAccountEvaluationSettings: getSettings,
  updateAccountEvaluationSettings: updateSettings
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const defaults = { enabled: false, interval_hours: 12, concurrency: 3, model_id: '', effort: 'low', revision: 'one' }

describe('AccountEvaluationAutomationSettings', () => {
  beforeEach(() => {
    getSettings.mockReset().mockResolvedValue({ ...defaults })
    updateSettings.mockReset().mockImplementation(async settings => ({ ...settings, revision: 'two' }))
  })

  it('loads the 12-hour default without enabling tests and saves explicit changes', async () => {
    const wrapper = mount(AccountEvaluationAutomationSettings)
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.get('[data-testid="auto-toggle"]').attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('[data-testid="auto-status"]').text()).toBe('common.disabled')
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    expect((wrapper.get('[data-testid="auto-interval"]').element as HTMLInputElement).value).toBe('12')
    expect((wrapper.get('[data-testid="auto-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="auto-enabled"]').setValue(true)
    await wrapper.get('[data-testid="auto-interval"]').setValue('24')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({ enabled: true, interval_hours: 24, concurrency: 3, model_id: 'gpt-6.1-sol' }), expect.any(AbortSignal))
    expect(wrapper.find('[data-testid="auto-model"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="auto-effort"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.evaluation.automation.saved')
    expect(wrapper.get('[data-testid="auto-status"]').text()).toBe('common.enabled')
    wrapper.unmount()
  })

  it('rejects out-of-range concurrency and intervals without sending a request', async () => {
    const wrapper = mount(AccountEvaluationAutomationSettings)
    await flushPromises()
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    for (const [field, value] of [['auto-concurrency', '0'], ['auto-concurrency', '11'], ['auto-interval', '0'], ['auto-interval', '169']]) {
      await wrapper.get('[data-testid="auto-concurrency"]').setValue('3')
      await wrapper.get('[data-testid="auto-interval"]').setValue('12')
      await wrapper.get(`[data-testid="${field}"]`).setValue(value)
      await wrapper.get('form').trigger('submit')
      expect(wrapper.get('[data-testid="auto-save"]').attributes('disabled')).toBeDefined()
    }
    expect(updateSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('clears the saved confirmation when settings have unsaved changes', async () => {
    const wrapper = mount(AccountEvaluationAutomationSettings)
    await flushPromises()
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    for (const [field, value] of [
      ['auto-interval', '24'],
      ['auto-enabled', true],
      ['auto-concurrency', '5']
    ] as const) {
      await wrapper.get('form').trigger('submit')
      await flushPromises()
      expect(wrapper.text()).toContain('admin.accounts.evaluation.automation.saved')
      await wrapper.get(`[data-testid="${field}"]`).setValue(value)
      expect(wrapper.text()).not.toContain('admin.accounts.evaluation.automation.saved')
    }
    wrapper.unmount()
  })

  it('blocks saving after a failed load, supports retry, and does not claim a failed save succeeded', async () => {
    getSettings.mockRejectedValueOnce(new Error('Offline'))
    updateSettings.mockRejectedValueOnce(new Error('Save failed'))
    const wrapper = mount(AccountEvaluationAutomationSettings)
    await flushPromises()
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('Offline')
    await wrapper.get('[data-testid="auto-retry"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Save failed')
    expect(wrapper.text()).not.toContain('admin.accounts.evaluation.automation.saved')
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    expect(wrapper.find('form').exists()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('Save failed')
    wrapper.unmount()
  })

  it('preserves unsaved fields while collapsed without changing the configured status', async () => {
    const wrapper = mount(AccountEvaluationAutomationSettings)
    await flushPromises()
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    await wrapper.get('[data-testid="auto-enabled"]').setValue(true)
    await wrapper.get('[data-testid="auto-interval"]').setValue('24')
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    expect(wrapper.get('[data-testid="auto-status"]').text()).toBe('common.disabled')
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    expect((wrapper.get('[data-testid="auto-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="auto-interval"]').element as HTMLInputElement).value).toBe('24')
    wrapper.unmount()
  })

  it('saves shared model options without enabling an unsaved automatic schedule', async () => {
    const wrapper = mount(defineComponent({
      components: { AccountEvaluationOptions, AccountEvaluationAutomationSettings },
      setup: () => ({ models: [{ id: 'custom-model', display_name: 'Custom model' }] }),
      template: '<AccountEvaluationOptions :models="models" /><AccountEvaluationAutomationSettings />',
    }))
    await flushPromises()
    expect(getSettings).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="auto-toggle"]').trigger('click')
    await wrapper.get('[data-testid="auto-enabled"]').setValue(true)
    await wrapper.get('[data-testid="auto-interval"]').setValue('24')
    await wrapper.get('[data-testid="evaluation-model"]').setValue('custom-model')
    await wrapper.get('[data-testid="evaluation-effort"]').setValue('high')
    await wrapper.get('[data-testid="evaluation-save-options"]').trigger('click')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith(expect.objectContaining({ enabled: false, interval_hours: 12, model_id: 'custom-model', effort: 'high' }), expect.any(AbortSignal))
    expect((wrapper.get('[data-testid="auto-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="auto-interval"]').element as HTMLInputElement).value).toBe('24')
    expect(wrapper.get('[data-testid="auto-status"]').text()).toBe('common.disabled')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(updateSettings).toHaveBeenLastCalledWith(expect.objectContaining({ enabled: true, interval_hours: 24, model_id: 'custom-model', effort: 'high' }), expect.any(AbortSignal))
    wrapper.unmount()
  })
})
