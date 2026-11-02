import { apiClient } from './client'

export interface ChannelCatalogModel {
  name: string
  platform: string
}

export interface ChannelCatalogGroup {
  id: number
  name: string
  platform: string
  rate_multiplier: number
  user_rate_multiplier?: number
  available_models: ChannelCatalogModel[]
  /** Monitor associations resolved by the server within the user's authorized groups. */
  monitor_ids?: number[]
}

export interface ChannelCatalogEntry {
  name: string
  description: string
  groups: ChannelCatalogGroup[]
}

/** Current user's authorized channel groups and their configured model support. */
export async function getChannelMonitorCatalog(signal?: AbortSignal): Promise<ChannelCatalogEntry[]> {
  const { data } = await apiClient.get<ChannelCatalogEntry[]>('/channel-monitors/catalog', { signal })
  return data
}
