import type { AccountPlatform, AccountType } from '@/types'

/** 与后端 AccountSupportsQuota 一致；OpenAI OAuth 支持本地美元预算。 */
export function supportsAccountQuota(type?: AccountType | null, platform?: AccountPlatform | null): boolean {
  return type === 'apikey' || type === 'bedrock' || type === 'service_account' ||
    (type === 'oauth' && platform === 'openai')
}
