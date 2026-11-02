import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))
import { getAccountEvaluationHistory } from '@/api/admin/accountEvaluationHistory'

describe('account evaluation batch history API', () => {
  beforeEach(() => get.mockReset())

  it('sends one cancellable request with IDs and preserves partial results', async () => {
    const response = { accounts: [{ id: 2, results: [], history_error: 'invalid_history' }], missing_ids: [1] }
    get.mockResolvedValue({ data: response })
    const signal = new AbortController().signal
    await expect(getAccountEvaluationHistory([1, 2, 2], signal)).resolves.toEqual(response)
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/admin/accounts/model-evaluation-history', { params: { ids: '1,2' }, signal })
  })

  it.each([[], [0], [-1], [1.5], Array.from({ length: 21 }, (_, index) => index + 1)].map(ids => ({ ids })))('rejects an invalid ID selection: $ids', async ({ ids }) => {
    await expect(getAccountEvaluationHistory(ids)).rejects.toThrow('1–20')
    expect(get).not.toHaveBeenCalled()
  })
})
