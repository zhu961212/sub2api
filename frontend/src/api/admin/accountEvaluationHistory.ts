import { apiClient } from '../client'
import type { Account } from '@/types'
import type { AccountEvaluationResult } from './accountEvaluation'

/** Only the fields needed by the evaluation dialog; never account credentials. */
export interface AccountEvaluationHistory {
  id: number
  name: string
  platform: Account['platform']
  type: Account['type']
  schedulable: boolean
  proxy_id: number | null
  proxy_name: string | null
  results: AccountEvaluationResult[]
  history_error?: 'invalid_history'
}

export interface AccountEvaluationTarget extends Omit<AccountEvaluationHistory, 'history_error'> {
  history_error?: AccountEvaluationHistory['history_error'] | 'unavailable' | 'missing_account'
  available?: boolean
}

export interface AccountEvaluationHistoryResponse {
  accounts: AccountEvaluationHistory[]
  missing_ids: number[]
}

export async function getAccountEvaluationHistory(ids: number[], signal?: AbortSignal): Promise<AccountEvaluationHistoryResponse> {
  const uniqueIDs = [...new Set(ids)]
  if (uniqueIDs.length === 0 || uniqueIDs.length > 20 || uniqueIDs.some(id => !Number.isSafeInteger(id) || id <= 0)) {
    throw new Error('Evaluation history requires 1–20 positive account IDs')
  }
  const { data } = await apiClient.get<AccountEvaluationHistoryResponse>('/admin/accounts/model-evaluation-history', {
    params: { ids: uniqueIDs.join(',') },
    signal
  })
  return data
}
