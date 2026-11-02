import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import type { ChannelCatalogGroup } from '@/api/channelMonitorCatalog'
import ChannelGroupDetails from '../ChannelGroupDetails.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

function group(overrides: Partial<ChannelCatalogGroup> = {}): ChannelCatalogGroup {
  return {
    id: 1,
    name: 'gpt 低价',
    platform: 'openai',
    rate_multiplier: 0.3,
    available_models: Array.from({ length: 7 }, (_, index) => ({ name: `model-${index + 1}`, platform: 'openai' })),
    ...overrides,
  }
}

describe('ChannelGroupDetails', () => {
  it('shows the complete available model list without an expansion step', () => {
    const wrapper = mount(ChannelGroupDetails, { props: { groups: [group()] } })
    for (let index = 1; index <= 7; index++) expect(wrapper.text()).toContain(`model-${index}`)
    expect(wrapper.findAll('button')).toHaveLength(0)
    expect(wrapper.get('[data-testid="catalog-rate-1"]').text()).toBe('×0.3')
  })

  it('uses the models label when the sole group repeats the monitor name', () => {
    const wrapper = mount(ChannelGroupDetails, { props: { groups: [group()], monitorName: 'GPT低价' } })
    expect(wrapper.text()).not.toContain('gpt 低价')
    expect(wrapper.text()).toContain('channelStatus.catalog.models')
  })

  it('preserves group names and their independent rates when several groups share a monitor', () => {
    const wrapper = mount(ChannelGroupDetails, {
      props: {
        groups: [group(), group({ id: 2, name: 'GPT stable', rate_multiplier: 0.5, user_rate_multiplier: 0 })],
        monitorName: 'GPT低价',
      },
    })
    expect(wrapper.text()).toContain('gpt 低价')
    expect(wrapper.text()).toContain('GPT stable')
    const customGroup = wrapper.get('[data-testid="catalog-group-2"]')
    expect(customGroup.get('[data-testid="catalog-rate-2"]').text()).toBe('×0')
    expect(customGroup.get('.line-through').text()).toBe('×0.5')
    expect(customGroup.text()).toContain('channelStatus.catalog.customRate')
  })

  it('keeps an independently named group and the empty-model state', () => {
    const wrapper = mount(ChannelGroupDetails, {
      props: { groups: [group({ available_models: [] })], monitorName: 'Key probe' },
    })
    expect(wrapper.text()).toContain('gpt 低价')
    expect(wrapper.text()).toContain('channelStatus.catalog.noModels')
  })
})
