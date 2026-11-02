import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, type EffectScope } from 'vue'
import { ACCOUNT_EVALUATION_EFFORTS, DEFAULT_ACCOUNT_EVALUATION_MODEL, accountEvaluationModelOptions, resolveAccountEvaluationModel, useAccountEvaluationSettings } from '../useAccountEvaluationModel'

const { getSettings, updateSettings } = vi.hoisted(() => ({ getSettings: vi.fn(), updateSettings: vi.fn() }))
vi.mock('@/api/admin/accountEvaluationSettings', () => ({ getAccountEvaluationSettings: getSettings, updateAccountEvaluationSettings: updateSettings }))

const scopes: EffectScope[] = []
function useSettings() {
  const scope = effectScope()
  scopes.push(scope)
  return scope.run(useAccountEvaluationSettings)!
}

describe('shared account evaluation settings', () => {
  const defaults = { enabled: true, interval_hours: 24, concurrency: 4, model_id: 'saved-model', effort: 'high' as const }
  beforeEach(() => {
    getSettings.mockReset().mockResolvedValue({ ...defaults })
    updateSettings.mockReset().mockImplementation(async settings => ({ ...settings }))
  })
  afterEach(() => {
    scopes.splice(0).forEach(scope => scope.stop())
  })

  it('loads the server configuration once and shares model, effort, and schedule across consumers', async () => {
    const manual = useSettings()
    const automatic = useSettings()
    await Promise.all([manual.load(), automatic.load()])
    expect(getSettings).toHaveBeenCalledTimes(1)
    expect(manual.model.value).toBe('saved-model')
    expect(manual.effort.value).toBe('high')
    manual.model.value = 'another-model'
    manual.effort.value = 'medium'
    expect(automatic.form.model_id).toBe('another-model')
    expect(automatic.form.effort).toBe('medium')
    expect(await manual.save()).toBe(true)
    expect(updateSettings).toHaveBeenCalledWith({ ...defaults, model_id: 'another-model', effort: 'medium' }, expect.any(AbortSignal))
    expect(automatic.saved.value).toBe(true)
  })

  it.each(['', '   ', 'x'.repeat(201), '{"model":"gpt-6.1-sol"}', 'invalid model', 'model\u0000'])('normalizes invalid legacy server models: %s', async value => {
    getSettings.mockResolvedValue({ ...defaults, model_id: value })
    const settings = useSettings()
    await settings.load()
    expect(settings.model.value).toBe(DEFAULT_ACCOUNT_EVALUATION_MODEL)
    expect(resolveAccountEvaluationModel(value)).toBe(DEFAULT_ACCOUNT_EVALUATION_MODEL)
  })

  it('retains configured models in the dropdown even when an account cannot list them', () => {
    const options = accountEvaluationModelOptions([{ id: 'available-model', display_name: 'Available' }, { id: 'gpt-image-2', display_name: 'Image' }], 'saved-model')
    expect(options.map(option => option.id)).toEqual([DEFAULT_ACCOUNT_EVALUATION_MODEL, 'available-model', 'saved-model'])
    expect(new Set(accountEvaluationModelOptions([], DEFAULT_ACCOUNT_EVALUATION_MODEL).map(option => option.id)).size).toBe(1)
    expect(ACCOUNT_EVALUATION_EFFORTS).toEqual(['none', 'minimal', 'low', 'medium', 'high', 'xhigh'])
  })

  it('blocks writes after a failed read and supports retry without overwriting server defaults', async () => {
    getSettings.mockRejectedValueOnce(new Error('Offline'))
    const settings = useSettings()
    await settings.load()
    expect(settings.loaded.value).toBe(false)
    expect(await settings.save()).toBe(false)
    expect(updateSettings).not.toHaveBeenCalled()
    await settings.load()
    expect(settings.loaded.value).toBe(true)
    expect(settings.model.value).toBe('saved-model')
    expect(settings.error.value).toBeNull()
  })

  it('keeps an empty server model dirty until the default choice is explicitly persisted', async () => {
    getSettings.mockResolvedValue({ ...defaults, model_id: '' })
    const settings = useSettings()
    await settings.load()
    expect(settings.model.value).toBe(DEFAULT_ACCOUNT_EVALUATION_MODEL)
    expect(settings.optionsDirty.value).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()
    expect(await settings.saveOptions()).toBe(true)
    expect(updateSettings).toHaveBeenCalledWith({ ...defaults, model_id: DEFAULT_ACCOUNT_EVALUATION_MODEL }, expect.any(AbortSignal))
    expect(settings.optionsDirty.value).toBe(false)
  })

  it('does not update the server revision when model and effort match the saved configuration', async () => {
    const settings = useSettings()
    await settings.load()
    expect(settings.optionsDirty.value).toBe(false)
    expect(await settings.saveOptions()).toBe(true)
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('cancels the shared request only when its final consumer leaves', async () => {
    let signal!: AbortSignal
    getSettings.mockImplementation((value: AbortSignal) => new Promise((_, reject) => {
      signal = value
      value.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
    }))
    const first = useSettings()
    useSettings()
    const pending = first.load()
    scopes[0]!.stop()
    expect(signal.aborted).toBe(false)
    scopes[1]!.stop()
    expect(signal.aborted).toBe(true)
    await pending
  })
})
