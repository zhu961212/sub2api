import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getChannelMonitorCatalog } from '../channelMonitorCatalog'

afterEach(() => vi.restoreAllMocks())

describe('channel monitor catalog', () => {
  it('uses the monitor catalog endpoint and forwards cancellation', async () => {
    const catalog = [{ name: 'Channel', description: '', groups: [] }]
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: catalog })
    const signal = new AbortController().signal
    expect(await getChannelMonitorCatalog(signal)).toEqual(catalog)
    expect(get).toHaveBeenCalledWith('/channel-monitors/catalog', { signal })
  })
})
