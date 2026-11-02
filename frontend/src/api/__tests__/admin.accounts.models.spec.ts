import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getAvailableModels } from '../admin/accounts'

afterEach(() => vi.restoreAllMocks())

describe('admin account model discovery', () => {
  it('forwards cancellation so abandoned evaluation pages stop discovery requests', async () => {
    const models = [{ id: 'gpt-6.1-sol', display_name: 'GPT-6.1 Sol' }]
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: models })
    const controller = new AbortController()
    expect(await getAvailableModels(7, controller.signal)).toEqual(models)
    expect(get).toHaveBeenCalledWith('/admin/accounts/7/models', { signal: controller.signal })
  })
})
