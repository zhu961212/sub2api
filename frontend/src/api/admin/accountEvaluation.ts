import { buildApiUrl } from '../url'
import { ADMIN_UI_REQUEST_HEADER } from '../adminUIRequest'
import type { Account } from '@/types'

export type EvaluationKind = 'candy' | 'pelican'
export type EvaluationEffort = 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh'

export interface AccountEvaluationResult {
  id: string
  kind: EvaluationKind
  model: string
  effort: EvaluationEffort
  status: 'correct' | 'wrong' | 'completed' | 'failed'
  answer: string
  svg?: string
  error?: string
  duration_ms: number
  tested_at: string
  input_tokens?: number
  output_tokens?: number
  reasoning_tokens?: number
  source?: 'manual' | 'automatic'
  scheduling_stopped?: boolean
  scheduling_stopped_at?: string
  scheduling_resumed?: boolean
  scheduling_resumed_at?: string
  scheduling_resume_reason?: string
  scheduling_stop_reason?: string
  proxy_id?: number | null
  proxy_name?: string
  run: number
  runs: number
}

export interface AccountEvaluationRequest {
  kind: EvaluationKind
  model_id: string
  effort: EvaluationEffort
  runs: number
}

export interface AccountEvaluationEvent {
  type: string
  code?: string
  text?: string
  error?: string
  success?: boolean
  data?: AccountEvaluationResult | { run: number; runs: number }
}

export function supportsAccountEvaluation(account: Pick<Account, 'platform' | 'type'>): boolean {
  return account.platform === 'openai' && account.type === 'oauth'
}

export function isEvaluationResult(value: unknown): value is AccountEvaluationResult {
  if (!value || typeof value !== 'object') return false
  const result = value as Partial<AccountEvaluationResult>
  return typeof result.id === 'string' &&
    (result.kind === 'candy' || result.kind === 'pelican') &&
    ['correct', 'wrong', 'completed', 'failed'].includes(result.status ?? '') &&
    typeof result.answer === 'string' && typeof result.model === 'string' &&
    typeof result.tested_at === 'string' && typeof result.effort === 'string' &&
    typeof result.duration_ms === 'number' && Number.isFinite(result.duration_ms)
}

/** Stream one account's evaluation. A truncated stream is never a successful test. */
export async function evaluateAccount(
  accountID: number,
  request: AccountEvaluationRequest,
  onEvent: (event: AccountEvaluationEvent) => void,
  signal: AbortSignal
): Promise<void> {
  const token = localStorage.getItem('auth_token')
  const response = await fetch(buildApiUrl(`/admin/accounts/${accountID}/model-evaluation`), {
    method: 'POST',
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      [ADMIN_UI_REQUEST_HEADER]: '1',
      ...(token ? { Authorization: `Bearer ${token}` } : {})
    },
    body: JSON.stringify(request),
    signal
  })
  if (!response.ok) {
    const error = await response.json().catch(() => null)
    throw new Error(typeof error?.message === 'string' ? error.message : `HTTP ${response.status}`)
  }
  const reader = response.body?.getReader()
  if (!reader) throw new Error('Missing evaluation response stream')

  const decoder = new TextDecoder()
  let buffer = ''
  let dataLines: string[] = []
  let complete = false
  const dispatch = () => {
    const payload = dataLines.join('\n')
    dataLines = []
    if (!payload || payload === '[DONE]') return
    const event = JSON.parse(payload) as AccountEvaluationEvent
    if (event.type === 'error') throw new Error(event.error || event.text || 'Evaluation failed')
    if (event.type === 'evaluation_result' && !isEvaluationResult(event.data)) {
      throw new Error('Invalid evaluation result')
    }
    if (event.type === 'test_complete') {
      if (!event.success) throw new Error(event.error || 'Evaluation failed')
      complete = true
    }
    onEvent(event)
  }
  const consumeLine = (line: string) => {
    line = line.replace(/\r$/, '')
    if (line === '') dispatch()
    else if (line.startsWith('data:')) dataLines.push(line.slice(5).replace(/^ /, ''))
  }

  try {
    while (!complete) {
      const { done, value } = await reader.read()
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
      // The backend bounds output; also reject an unbounded/malformed SSE frame.
      if (buffer.length + dataLines.join('').length > 2 * 1024 * 1024) {
        throw new Error('Evaluation response is too large')
      }
      let newline: number
      while ((newline = buffer.indexOf('\n')) >= 0) {
        consumeLine(buffer.slice(0, newline))
        buffer = buffer.slice(newline + 1)
      }
      if (done) {
        if (buffer) consumeLine(buffer)
        dispatch()
        break
      }
    }
    signal.throwIfAborted()
    if (!complete) throw new Error('Evaluation stream ended before completion')
  } finally {
    await reader.cancel().catch(() => undefined)
    reader.releaseLock()
  }
}
