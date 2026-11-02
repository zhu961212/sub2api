import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getChannelMonitorCatalog, type ChannelCatalogEntry } from '@/api/channelMonitorCatalog'
import { extractApiErrorMessage } from '@/utils/apiError'

export function useChannelMonitorCatalog() {
  const { t } = useI18n()
  const channels = ref<ChannelCatalogEntry[]>([])
  const loading = ref(false)
  const error = ref('')
  let request: AbortController | null = null

  async function reload(): Promise<void> {
    request?.abort()
    const current = new AbortController()
    request = current
    loading.value = true
    error.value = ''
    try {
      const data = await getChannelMonitorCatalog(current.signal)
      if (!current.signal.aborted) channels.value = data
    } catch (cause) {
      if (!current.signal.aborted) error.value = extractApiErrorMessage(cause, t('channelStatus.catalog.loadError'))
    } finally {
      if (request === current) loading.value = false
    }
  }

  onBeforeUnmount(() => request?.abort())
  return { channels, loading, error, reload }
}
