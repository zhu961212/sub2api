import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ChannelGroupCatalog from '../ChannelGroupCatalog.vue'
import type { ChannelCatalogEntry } from '@/api/channelMonitorCatalog'

const { getCatalog } = vi.hoisted(() => ({ getCatalog: vi.fn() }))
vi.mock('@/api/channelMonitorCatalog', () => ({ getChannelMonitorCatalog: getCatalog }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key })
}))
enableAutoUnmount(afterEach)

const catalog: ChannelCatalogEntry[] = [{
  name: 'Alpha', description: 'Channel description', groups: [
    { id: 1, name: 'Standard', platform: 'openai', rate_multiplier: 1.5, user_rate_multiplier: 0, available_models: [{ name: 'gpt-6.1-sol', platform: 'openai' }] },
    { id: 2, name: 'Reasoning', platform: 'openai', rate_multiplier: 2, available_models: Array.from({ length: 6 }, (_, index) => ({ name: `model-${index + 1}`, platform: 'openai' })) },
    { id: 3, name: 'Empty', platform: 'gemini', rate_multiplier: 1, available_models: [] }
  ]
}]

beforeEach(() => { getCatalog.mockReset().mockResolvedValue(catalog) })

describe('ChannelGroupCatalog', () => {
  it('shows the actual channel, group-specific models, and effective zero user rate', async () => {
    const wrapper = mount(ChannelGroupCatalog)
    await flushPromises()
    expect(wrapper.text()).toContain('Alpha')
    expect(wrapper.text()).toContain('Channel description')
    expect(wrapper.get('[data-testid="catalog-rate-1"]').text()).toBe('×0')
    expect(wrapper.get('[data-testid="catalog-rate-2"]').text()).toBe('×2')
    expect(wrapper.get('[data-testid="catalog-group-1"]').text()).toContain('gpt-6.1-sol')
    expect(wrapper.get('[data-testid="catalog-group-1"]').text()).not.toContain('model-1')
    expect(wrapper.get('[data-testid="catalog-group-2"]').text()).not.toContain('gpt-6.1-sol')
    expect(wrapper.get('[data-testid="catalog-group-3"]').text()).toContain('channelStatus.catalog.noModels')
  })

  it('expands only the selected group’s additional models', async () => {
    const wrapper = mount(ChannelGroupCatalog)
    await flushPromises()
    const group = wrapper.get('[data-testid="catalog-group-2"]')
    expect(group.text()).toContain('model-4')
    expect(group.text()).not.toContain('model-5')
    await wrapper.get('[data-testid="catalog-models-2"]').trigger('click')
    expect(group.text()).toContain('model-6')
    expect(wrapper.get('[data-testid="catalog-models-2"]').attributes('aria-expanded')).toBe('true')
    await wrapper.get('[data-testid="catalog-models-2"]').trigger('click')
    expect(group.text()).not.toContain('model-5')
  })

  it('shows an independent error and retries without affecting monitoring', async () => {
    getCatalog.mockRejectedValueOnce(new Error('Catalog unavailable'))
    const wrapper = mount(ChannelGroupCatalog)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Catalog unavailable')
    expect(wrapper.text()).not.toContain('channelStatus.catalog.empty')
    await wrapper.get('[data-testid="catalog-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Alpha')
  })

  it('shows the empty state when no accessible configured channels exist', async () => {
    getCatalog.mockResolvedValueOnce([])
    const wrapper = mount(ChannelGroupCatalog)
    await flushPromises()
    expect(wrapper.text()).toContain('channelStatus.catalog.empty')
    expect(wrapper.find('[data-testid="catalog-channel"]').exists()).toBe(false)
  })

  it('cancels the pending catalog request when leaving the page', async () => {
    let requestSignal: AbortSignal | undefined
    getCatalog.mockImplementation((signal: AbortSignal) => new Promise((_, reject) => {
      requestSignal = signal
      signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const wrapper = mount(ChannelGroupCatalog)
    wrapper.unmount()
    await flushPromises()
    expect(requestSignal?.aborted).toBe(true)
  })
})
