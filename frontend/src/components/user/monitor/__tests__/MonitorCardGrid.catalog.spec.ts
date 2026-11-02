import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import MonitorCardGrid from '../MonitorCardGrid.vue'
import type { ChannelCatalogEntry, ChannelCatalogGroup } from '@/api/channelMonitorCatalog'
import type { UserMonitorView } from '@/api/channelMonitor'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorQuotaVisible: () => false }))
enableAutoUnmount(afterEach)

const item: UserMonitorView = {
  id: 10, name: 'GPT stable', provider: 'openai', group_name: 'display label only',
  primary_model: 'gpt-6-astra', primary_status: 'operational', primary_latency_ms: 200,
  primary_ping_latency_ms: 80, availability_7d: 99.9, extra_models: [], timeline: [],
}
function group(overrides: Partial<ChannelCatalogGroup> = {}): ChannelCatalogGroup {
  return {
    id: 1, name: 'GPT group', platform: 'openai', rate_multiplier: 2, user_rate_multiplier: 0,
    available_models: Array.from({ length: 6 }, (_, index) => ({ name: `model-${index + 1}`, platform: 'openai' })),
    monitor_ids: [10], ...overrides,
  }
}
function mountGrid(channels: ChannelCatalogEntry[], items = [item]) {
  return mount(MonitorCardGrid, {
    props: { items, channels, window: '7d', countdownSeconds: 60, loading: false, detailCache: {} },
    global: { stubs: { MonitorMetricPair: true, MonitorAvailabilityRow: true, MonitorTimeline: true, ProviderIcon: true } },
  })
}

describe('monitor cards with group rates and available models', () => {
  it('places effective rates and models in their associated status card', () => {
    const wrapper = mountGrid([{ name: 'Channel', description: '', groups: [group()] }])
    const card = wrapper.get('[data-testid="monitor-card-10"]')
    expect(card.text()).toContain('GPT stable')
    expect(card.text()).toContain('GPT group')
    expect(card.get('[data-testid="catalog-rate-1"]').text()).toBe('×0')
    expect(card.text()).toContain('model-6')
    expect(wrapper.find('[data-testid="catalog-channel"]').exists()).toBe(false)
    expect(wrapper.find('button button').exists()).toBe(false)
  })

  it('shows all supported models and opens status details from the status area', async () => {
    const wrapper = mountGrid([{ name: 'Channel', description: '', groups: [group()] }])
    expect(wrapper.text()).toContain('model-6')
    expect(wrapper.find('[data-testid="catalog-models-1"]').exists()).toBe(false)
    expect(wrapper.emitted('cardClick')).toBeUndefined()
    await wrapper.get('[data-testid="monitor-card-10"] > button').trigger('click')
    expect(wrapper.emitted('cardClick')?.[0]).toEqual([item])
  })

  it('merges a shared group’s model lists from multiple channels without duplicates', () => {
    const wrapper = mountGrid([
      { name: 'A', description: '', groups: [group({ available_models: [{ name: 'model-a', platform: 'openai' }] })] },
      { name: 'B', description: '', groups: [group({ available_models: [{ name: 'model-a', platform: 'openai' }, { name: 'model-b', platform: 'openai' }] })] },
    ])
    expect(wrapper.findAll('[data-testid="catalog-group-1"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="catalog-group-1"]').text()).toContain('model-b')
    expect(wrapper.findAll('span').filter(span => span.text() === 'model-a')).toHaveLength(1)
  })

  it('ignores unassociated groups and never guesses by names', () => {
    const wrapper = mountGrid([{ name: item.name, description: '', groups: [group({ name: item.group_name, monitor_ids: [] })] }])
    expect(wrapper.get('[data-testid="monitor-card-10"]').find('[data-testid="channel-group-details"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="catalog-channel"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('model-1')
  })

  it('shows the monitor empty state when no monitors have been configured', () => {
    const wrapper = mountGrid([{ name: 'Group only', description: '', groups: [group({ monitor_ids: [] })] }], [])
    expect(wrapper.find('[data-testid="catalog-channel"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'EmptyState' }).exists()).toBe(true)
  })

  it('keeps exactly the three monitored channels when the catalog contains additional groups', () => {
    const items = [item, { ...item, id: 20, name: 'GPT low cost' }, { ...item, id: 30, name: 'DeepSeek', provider: 'deepseek' as const }]
    const wrapper = mountGrid([
      { name: 'GPT stable', description: '', groups: [group()] },
      { name: 'GPT low cost', description: '', groups: [group({ id: 2, monitor_ids: [20] })] },
      { name: 'DeepSeek', description: '', groups: [group({ id: 3, platform: 'deepseek', monitor_ids: [30] })] },
      { name: 'Unmonitored image channel', description: '', groups: [group({ id: 4, monitor_ids: [] })] },
      { name: 'Inactive monitor', description: '', groups: [group({ id: 5, monitor_ids: [40] })] },
    ], items)
    expect(wrapper.findAll('[data-testid^="monitor-card-"]')).toHaveLength(3)
    expect(wrapper.findAll('[data-testid="catalog-channel"]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('Unmonitored image channel')
    expect(wrapper.find('[data-testid="catalog-group-4"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="catalog-group-5"]').exists()).toBe(false)
  })

  it('keeps monitor loading independent from whether the catalog has already loaded', async () => {
    const wrapper = mountGrid([{ name: 'Group only', description: '', groups: [group()] }], [])
    await wrapper.setProps({ loading: true })
    expect(wrapper.findAll('.animate-pulse')).toHaveLength(6)
    expect(wrapper.findComponent({ name: 'EmptyState' }).exists()).toBe(false)
    expect(wrapper.find('[data-testid="catalog-channel"]').exists()).toBe(false)
  })
})
