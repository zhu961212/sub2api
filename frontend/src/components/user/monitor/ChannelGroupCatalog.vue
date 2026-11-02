<template>
  <section class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800" data-testid="channel-group-catalog">
    <header class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('channelStatus.catalog.title') }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('channelStatus.catalog.description') }}</p>
      </div>
      <button type="button" class="btn btn-secondary btn-sm shrink-0 !px-2" :disabled="loading" :aria-label="t('common.refresh')" :title="t('common.refresh')" data-testid="catalog-refresh" @click="reload">
        <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" aria-hidden="true" />
      </button>
    </header>
    <div v-if="error" class="flex flex-wrap items-center gap-3 px-4 py-3 text-xs text-amber-700 dark:text-amber-400" role="alert">
      <span>{{ error }}</span>
      <button type="button" class="underline" :disabled="loading" data-testid="catalog-retry" @click="reload">{{ t('channelStatus.catalog.retry') }}</button>
    </div>
    <p v-if="loading && !loaded" class="px-4 py-6 text-center text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
    <p v-else-if="!channels.length && !error" class="px-4 py-6 text-center text-sm text-gray-500 dark:text-gray-400">{{ t('channelStatus.catalog.empty') }}</p>
    <div v-if="channels.length" class="divide-y divide-gray-100 dark:divide-dark-700">
      <article v-for="(channel, index) in channels" :key="`${channel.name}-${index}`" class="grid gap-3 px-4 py-4 md:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]" data-testid="catalog-channel">
        <div class="min-w-0">
          <h3 class="break-words text-sm font-medium text-gray-900 dark:text-white">{{ channel.name }}</h3>
          <p v-if="channel.description" class="mt-1 break-words text-xs leading-5 text-gray-500 dark:text-gray-400">{{ channel.description }}</p>
        </div>
        <div class="min-w-0 space-y-3">
          <div v-for="group in channel.groups" :key="group.id" class="min-w-0" :data-testid="`catalog-group-${group.id}`">
            <div class="flex flex-wrap items-center gap-2 text-xs">
              <span class="font-medium text-gray-800 dark:text-gray-200">{{ group.name }}</span>
              <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] uppercase text-gray-500 dark:bg-dark-700 dark:text-gray-400">{{ group.platform }}</span>
              <span class="inline-flex items-center gap-1.5 rounded-md bg-primary-50 px-2 py-0.5 font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300" :aria-label="t('channelStatus.catalog.rate')">
                <span v-if="hasCustomRate(group)" class="text-gray-400 line-through">×{{ group.rate_multiplier }}</span>
                <span :data-testid="`catalog-rate-${group.id}`">×{{ group.user_rate_multiplier ?? group.rate_multiplier }}</span>
                <span v-if="hasCustomRate(group)" class="text-[10px]">{{ t('channelStatus.catalog.customRate') }}</span>
              </span>
            </div>
            <div class="mt-2 flex flex-wrap items-center gap-1.5" :aria-label="t('channelStatus.catalog.models')">
              <span v-for="model in visibleModels(index, group)" :key="`${model.platform}-${model.name}`" class="max-w-full break-all rounded-md border border-gray-200 px-2 py-0.5 font-mono text-[11px] text-gray-600 dark:border-dark-600 dark:text-gray-300" :title="model.platform">{{ model.name }}</span>
              <span v-if="!group.available_models.length" class="text-xs text-gray-400">{{ t('channelStatus.catalog.noModels') }}</span>
              <button v-if="group.available_models.length > previewCount" type="button" class="px-1 text-xs text-primary-600 hover:underline dark:text-primary-400" :aria-expanded="expanded.has(groupKey(index, group.id))" :data-testid="`catalog-models-${group.id}`" @click="toggleModels(index, group.id)">
                {{ expanded.has(groupKey(index, group.id)) ? t('common.collapse') : t('channelStatus.catalog.moreModels', { count: group.available_models.length - previewCount }) }}
              </button>
            </div>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getChannelMonitorCatalog, type ChannelCatalogEntry, type ChannelCatalogGroup } from '@/api/channelMonitorCatalog'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const previewCount = 4
const channels = ref<ChannelCatalogEntry[]>([])
const expanded = ref(new Set<string>())
const loading = ref(false)
const loaded = ref(false)
const error = ref('')
let request: AbortController | null = null

function groupKey(channelIndex: number, groupID: number): string { return `${channelIndex}-${groupID}` }
function hasCustomRate(group: ChannelCatalogGroup): boolean {
  return group.user_rate_multiplier != null && group.user_rate_multiplier !== group.rate_multiplier
}
function visibleModels(channelIndex: number, group: ChannelCatalogGroup) {
  return expanded.value.has(groupKey(channelIndex, group.id)) ? group.available_models : group.available_models.slice(0, previewCount)
}
function toggleModels(channelIndex: number, groupID: number) {
  const key = groupKey(channelIndex, groupID)
  if (expanded.value.has(key)) expanded.value.delete(key)
  else expanded.value.add(key)
}

async function reload(): Promise<void> {
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  error.value = ''
  try {
    const data = await getChannelMonitorCatalog(current.signal)
    if (current.signal.aborted) return
    channels.value = data
    loaded.value = true
  } catch (cause) {
    if (!current.signal.aborted) error.value = extractApiErrorMessage(cause, t('channelStatus.catalog.loadError'))
  } finally {
    if (request === current) loading.value = false
  }
}

defineExpose({ reload })
onMounted(reload)
onUnmounted(() => request?.abort())
</script>
