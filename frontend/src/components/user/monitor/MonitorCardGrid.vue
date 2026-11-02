<template>
  <div>
    <div
      v-if="loading && items.length === 0"
      class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3"
    >
      <div
        v-for="i in 6"
        :key="i"
        class="p-5 rounded-2xl min-h-[280px] bg-white/70 dark:bg-dark-800/60 border border-gray-200/80 dark:border-dark-700/70 animate-pulse"
      >
        <div class="flex items-start gap-3">
          <div class="w-9 h-9 rounded-xl bg-gray-200 dark:bg-dark-700"></div>
          <div class="flex-1 space-y-2">
            <div class="h-4 w-2/3 rounded bg-gray-200 dark:bg-dark-700"></div>
            <div class="h-3 w-1/2 rounded bg-gray-200 dark:bg-dark-700"></div>
          </div>
          <div class="h-6 w-16 rounded-full bg-gray-200 dark:bg-dark-700"></div>
        </div>
        <div class="mt-5 grid grid-cols-2 gap-2">
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-900/40"></div>
        </div>
        <div class="mt-6 h-5 w-full rounded bg-gray-100 dark:bg-dark-900/40"></div>
      </div>
    </div>

    <EmptyState
      v-else-if="items.length === 0"
      :title="t('channelStatus.empty.title')"
      :description="t('channelStatus.empty.description')"
    />

    <div
      v-else
      class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3"
    >
      <MonitorCard
        v-for="item in items"
        :key="item.id"
        :item="item"
        :groups="groupsForMonitor(item.id)"
        :window="window"
        :availability-value="resolveAvailability(item)"
        :countdown-seconds="countdownSeconds"
        @click="emit('cardClick', item)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UserMonitorView, UserMonitorDetail } from '@/api/channelMonitor'
import type { ChannelCatalogEntry, ChannelCatalogGroup } from '@/api/channelMonitorCatalog'
import EmptyState from '@/components/common/EmptyState.vue'
import MonitorCard from './MonitorCard.vue'

const props = defineProps<{
  items: UserMonitorView[]
  window: '7d' | '15d' | '30d'
  countdownSeconds: number
  loading: boolean
  detailCache: Record<number, UserMonitorDetail>
  channels?: ChannelCatalogEntry[]
}>()

const emit = defineEmits<{
  (e: 'cardClick', item: UserMonitorView): void
}>()

const { t } = useI18n()

const monitorGroups = computed(() => {
  const result = new Map<number, Map<number, ChannelCatalogGroup>>()
  for (const channel of props.channels ?? []) {
    for (const group of channel.groups) {
      for (const monitorID of group.monitor_ids ?? []) {
        if (!result.has(monitorID)) result.set(monitorID, new Map())
        const groups = result.get(monitorID)!
        // A group can belong to multiple channels; retain all configured models.
        const existing = groups.get(group.id)
        if (!existing) groups.set(group.id, { ...group, available_models: [...group.available_models] })
        else {
          const models = new Set(existing.available_models.map(model => `${model.platform}:${model.name}`))
          for (const model of group.available_models) {
            const key = `${model.platform}:${model.name}`
            if (!models.has(key)) {
              existing.available_models.push(model)
              models.add(key)
            }
          }
        }
      }
    }
  }
  return result
})

function groupsForMonitor(id: number): ChannelCatalogGroup[] {
  return [...(monitorGroups.value.get(id)?.values() ?? [])]
}

function resolveAvailability(item: UserMonitorView): number | null {
  if (props.window === '7d') {
    return item.availability_7d ?? null
  }
  const detail = props.detailCache[item.id]
  if (!detail) return null
  const primary = detail.models.find(m => m.model === item.primary_model)
  if (!primary) return null
  return props.window === '15d' ? primary.availability_15d ?? null : primary.availability_30d ?? null
}
</script>
