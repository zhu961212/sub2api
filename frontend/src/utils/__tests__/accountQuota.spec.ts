import { describe, expect, it } from 'vitest'
import { supportsAccountQuota } from '../accountQuota'

describe('local account quota eligibility', () => {
  it('enables OpenAI OAuth without enabling other OAuth providers', () => {
    expect(supportsAccountQuota('oauth', 'openai')).toBe(true)
    expect(supportsAccountQuota('oauth', 'anthropic')).toBe(false)
    expect(supportsAccountQuota('oauth', 'gemini')).toBe(false)
    expect(supportsAccountQuota('setup-token', 'openai')).toBe(false)
    expect(supportsAccountQuota('apikey', 'openai')).toBe(true)
    expect(supportsAccountQuota('service_account', 'gemini')).toBe(true)
  })
})
