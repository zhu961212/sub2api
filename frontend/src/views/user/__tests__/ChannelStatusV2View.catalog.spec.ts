import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import ChannelStatusV2View from '../ChannelStatusV2View.vue'

const { viewer, reloadCatalog, getSnapshot, getMatrix } = vi.hoisted(() => ({
  viewer: { isAdmin: false }, reloadCatalog: vi.fn(), getSnapshot: vi.fn(), getMatrix: vi.fn()
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => viewer }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/utils/featureFlags', () => ({ isChannelMonitorThroughputHidden: () => false, isChannelMonitorUserRankingHidden: () => false }))
vi.mock('@/api/channelMonitorV2', () => ({
  getDimensions: vi.fn().mockResolvedValue({ platforms: [], groups: [], models: [] }), getSnapshot, getMatrix
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => false, locale: { value: 'en' } })
}))
enableAutoUnmount(afterEach)
beforeEach(() => {
  viewer.isAdmin = false
  reloadCatalog.mockReset()
  getSnapshot.mockReset().mockImplementation(() => new Promise(() => undefined))
  getMatrix.mockReset().mockImplementation(() => new Promise(() => undefined))
})

function mountView() {
  return shallowMount(ChannelStatusV2View, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    ChannelGroupCatalog: defineComponent({
      setup(_, { expose }) { expose({ reload: reloadCatalog }) }, template: '<section data-testid="catalog" />'
    })
  } } })
}

describe('ChannelStatusV2 channel catalog', () => {
  it('renders catalog for users while metrics load independently', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="catalog"]').exists()).toBe(true)
    expect(getSnapshot).toHaveBeenCalled()
  })

  it('keeps the shared admin metrics screen free of user catalog requests', async () => {
    viewer.isAdmin = true
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="catalog"]').exists()).toBe(false)
  })
})
