import { apiClient } from '../client'
import type { EvaluationEffort } from './accountEvaluation'

export interface AccountEvaluationSettings {
  enabled: boolean
  interval_hours: number
  concurrency: number
  model_id: string
  effort: EvaluationEffort
  revision?: string
}

const endpoint = '/admin/accounts/model-evaluation-settings'

export async function getAccountEvaluationSettings(signal?: AbortSignal): Promise<AccountEvaluationSettings> {
  const { data } = await apiClient.get<AccountEvaluationSettings>(endpoint, { signal })
  return data
}

export async function updateAccountEvaluationSettings(settings: AccountEvaluationSettings, signal?: AbortSignal): Promise<AccountEvaluationSettings> {
  const { enabled, interval_hours, concurrency, model_id, effort } = settings
  const { data } = await apiClient.put<AccountEvaluationSettings>(endpoint, {
    enabled, interval_hours, concurrency, model_id, effort
  }, { signal })
  return data
}
