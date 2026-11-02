import { apiClient } from '../client'

export interface OpenAIDailyQuotaPolicy {
  enabled: boolean
  reset_hour: number
  reset_timezone: string
  plan_limits: Record<string, number>
  plan_5h_limits?: Record<string, number>
}

export interface OpenAIDailyQuotaPlan {
  plan_type: string
  account_count: number
}

export interface OpenAIDailyQuotaResponse {
  policy: OpenAIDailyQuotaPolicy
  plans: OpenAIDailyQuotaPlan[]
  account_count: number
  applied_count?: number
}

export async function getOpenAIDailyQuota(): Promise<OpenAIDailyQuotaResponse> {
  const { data } = await apiClient.get<OpenAIDailyQuotaResponse>('/admin/openai-daily-quota')
  return data
}

export async function updateOpenAIDailyQuota(policy: OpenAIDailyQuotaPolicy): Promise<OpenAIDailyQuotaResponse> {
  const { data } = await apiClient.put<OpenAIDailyQuotaResponse>('/admin/openai-daily-quota', policy)
  return data
}
