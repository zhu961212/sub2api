import { computed, onScopeDispose, reactive, ref, shallowRef } from 'vue'
import { createSharedComposable } from '@vueuse/core'
import { getAccountEvaluationSettings, updateAccountEvaluationSettings, type AccountEvaluationSettings } from '@/api/admin/accountEvaluationSettings'
import type { EvaluationEffort } from '@/api/admin/accountEvaluation'
import type { ClaudeModel } from '@/types'

export const DEFAULT_ACCOUNT_EVALUATION_MODEL = 'gpt-6.1-sol'
export const ACCOUNT_EVALUATION_EFFORTS: EvaluationEffort[] = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh']

export function resolveAccountEvaluationModel(value: unknown): string {
  if (typeof value !== 'string') return DEFAULT_ACCOUNT_EVALUATION_MODEL
  const model = value.trim()
  return /^[a-zA-Z0-9][a-zA-Z0-9._:/+-]{0,199}$/.test(model) ? model : DEFAULT_ACCOUNT_EVALUATION_MODEL
}

export function accountEvaluationModelOptions(available: ClaudeModel[], selected: string) {
  const options = new Map<string, Pick<ClaudeModel, 'id' | 'display_name'>>([
    [DEFAULT_ACCOUNT_EVALUATION_MODEL, { id: DEFAULT_ACCOUNT_EVALUATION_MODEL, display_name: 'GPT-6.1 Sol' }],
  ])
  for (const option of available) {
    if (!option.id.toLowerCase().startsWith('gpt-image-')) options.set(option.id, option)
  }
  if (!options.has(selected)) options.set(selected, { id: selected, display_name: selected })
  return [...options.values()]
}

/** Manual and scheduled evaluations use one server-persisted configuration. */
export const useAccountEvaluationSettings = createSharedComposable(() => {
  const form = reactive<AccountEvaluationSettings>({
    enabled: false, interval_hours: 12, concurrency: 3,
    model_id: DEFAULT_ACCOUNT_EVALUATION_MODEL, effort: 'low',
  })
  const configured = ref<AccountEvaluationSettings | null>(null)
  const loading = ref(false)
  const loaded = ref(false)
  const saving = ref(false)
  const saved = ref(false)
  const optionsSaved = ref(false)
  const error = shallowRef<unknown>(null)
  let controller: AbortController | null = null
  let pendingLoad: Promise<void> | null = null

  const model = computed({ get: () => form.model_id, set: (value: string) => { form.model_id = value; saved.value = false; optionsSaved.value = false } })
  const effort = computed({ get: () => form.effort, set: (value: EvaluationEffort) => { form.effort = value; saved.value = false; optionsSaved.value = false } })
  const configuredEnabled = computed(() => configured.value?.enabled ?? false)
  const optionsValid = computed(() => resolveAccountEvaluationModel(form.model_id) === form.model_id && ACCOUNT_EVALUATION_EFFORTS.includes(form.effort))
  const optionsDirty = computed(() => !!configured.value && (form.model_id !== configured.value.model_id || form.effort !== configured.value.effort))
  const valid = computed(() => Number.isInteger(form.interval_hours) && form.interval_hours >= 1 && form.interval_hours <= 168 &&
    Number.isInteger(form.concurrency) && form.concurrency >= 1 && form.concurrency <= 10 && optionsValid.value)

  function apply(settings: AccountEvaluationSettings) {
    const normalized = {
      ...settings, model_id: resolveAccountEvaluationModel(settings.model_id),
      effort: ACCOUNT_EVALUATION_EFFORTS.includes(settings.effort) ? settings.effort : 'low' as EvaluationEffort,
    }
    Object.assign(form, normalized)
    configured.value = { ...settings }
  }

  function load(): Promise<void> {
    if (loaded.value) return Promise.resolve()
    if (pendingLoad) return pendingLoad
    const current = new AbortController()
    controller = current
    loading.value = true
    error.value = null
    pendingLoad = (async () => {
      try {
        const settings = await getAccountEvaluationSettings(current.signal)
        if (current.signal.aborted) return
        apply(settings)
        loaded.value = true
      } catch (cause) {
        if (!current.signal.aborted) error.value = cause
      } finally {
        if (controller === current) {
          loading.value = false
          pendingLoad = null
        }
      }
    })()
    return pendingLoad
  }

  async function persist(settings: AccountEvaluationSettings, optionsOnly: boolean): Promise<boolean> {
    const current = new AbortController()
    controller = current
    saving.value = true
    saved.value = false
    optionsSaved.value = false
    error.value = null
    try {
      const updated = await updateAccountEvaluationSettings(settings, current.signal)
      if (current.signal.aborted) return false
      const scheduleDraft = { enabled: form.enabled, interval_hours: form.interval_hours, concurrency: form.concurrency }
      apply(updated)
      if (optionsOnly) Object.assign(form, scheduleDraft)
      saved.value = !optionsOnly
      optionsSaved.value = optionsOnly
      return true
    } catch (cause) {
      if (!current.signal.aborted) error.value = cause
      return false
    } finally {
      if (controller === current) saving.value = false
    }
  }

  function save(): Promise<boolean> {
    if (!loaded.value || !valid.value || saving.value) return Promise.resolve(false)
    return persist({ ...form }, false)
  }

  function saveOptions(): Promise<boolean> {
    if (!configured.value || !optionsValid.value || saving.value) return Promise.resolve(false)
    if (!optionsDirty.value) return Promise.resolve(true)
    return persist({ ...configured.value, model_id: model.value, effort: effort.value }, true)
  }

  onScopeDispose(() => controller?.abort())
  return { form, model, effort, loading, loaded, saving, saved, optionsSaved, error, valid, optionsValid, optionsDirty, configuredEnabled, load, save, saveOptions }
})
