import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { evaluateAccount, supportsAccountEvaluation } from '@/api/admin/accountEvaluation'

const result = {
  id: 'one', kind: 'candy', status: 'correct', answer: '21', model: 'gpt-5.4',
  effort: 'low', duration_ms: 100, tested_at: '2026-11-01T00:00:00Z', run: 1, runs: 1
}
const request = { kind: 'candy', model_id: 'gpt-5.4', effort: 'low', runs: 1 } as const

function streamingResponse(chunks: string[]) {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) {
      chunks.forEach(chunk => controller.enqueue(encoder.encode(chunk)))
      controller.close()
    }
  }), { headers: { 'Content-Type': 'text/event-stream' } })
}

describe('account evaluation stream', () => {
  beforeEach(() => { localStorage.setItem('auth_token', 'admin-token') })
  afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

  it('handles fragmented CRLF frames and the last unterminated event', async () => {
    const event = `data: ${JSON.stringify({ type: 'evaluation_result', data: result })}\r\n\r\n`
    const fetchMock = vi.fn().mockResolvedValue(streamingResponse([
      ': keepalive\r\n\r\n', event.slice(0, 24), event.slice(24),
      'data: {"type":"test_complete",\r\ndata: "success":true}'
    ]))
    vi.stubGlobal('fetch', fetchMock)
    const onEvent = vi.fn()
    await evaluateAccount(7, request, onEvent, new AbortController().signal)
    expect(onEvent).toHaveBeenCalledWith({ type: 'evaluation_result', data: result })
    expect(onEvent).toHaveBeenCalledWith({ type: 'test_complete', success: true })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/accounts/7/model-evaluation', expect.objectContaining({
      method: 'POST', credentials: 'include', body: JSON.stringify(request),
      headers: expect.objectContaining({ Authorization: 'Bearer admin-token' })
    }))
  })

  it('does not mark a truncated connection as complete', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(streamingResponse([
      `data: ${JSON.stringify({ type: 'evaluation_result', data: result })}\n\n`
    ])))
    await expect(evaluateAccount(7, request, vi.fn(), new AbortController().signal))
      .rejects.toThrow('before completion')
  })

  it('reports a server error without fabricating a wrong answer', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(streamingResponse([
      'data: {"type":"error","error":"Upstream request timed out"}\n\n'
    ])))
    const onEvent = vi.fn()
    await expect(evaluateAccount(7, request, onEvent, new AbortController().signal)).rejects.toThrow('timed out')
    expect(onEvent).not.toHaveBeenCalled()
  })

  it('rejects unsupported result schemas and propagates HTTP errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(streamingResponse([
      'data: {"type":"evaluation_result","data":{"status":"correct"}}\n\n'
    ])).mockResolvedValueOnce(new Response(JSON.stringify({ message: 'OAuth accounts only' }), { status: 400 })))
    await expect(evaluateAccount(7, request, vi.fn(), new AbortController().signal)).rejects.toThrow('Invalid evaluation result')
    await expect(evaluateAccount(7, request, vi.fn(), new AbortController().signal)).rejects.toThrow('OAuth accounts only')
  })

  it('supports OpenAI OAuth only', () => {
    expect(supportsAccountEvaluation({ platform: 'openai', type: 'oauth' })).toBe(true)
    expect(supportsAccountEvaluation({ platform: 'openai', type: 'apikey' })).toBe(false)
    expect(supportsAccountEvaluation({ platform: 'anthropic', type: 'oauth' })).toBe(false)
  })
})
