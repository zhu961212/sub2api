<template>
  <div class="space-y-3" data-testid="channel-group-details">
    <div v-for="group in groups" :key="group.id" class="min-w-0" :data-testid="`catalog-group-${group.id}`">
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs">
        <span class="min-w-0 break-words font-medium text-gray-600 dark:text-gray-300">{{ showGroupName(group) ? group.name : t('channelStatus.catalog.models') }}</span>
        <span class="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-primary-50 px-2 py-0.5 font-semibold text-primary-700 dark:bg-primary-900/30 dark:text-primary-300" :aria-label="t('channelStatus.catalog.rate')">
          <span v-if="hasCustomRate(group)" class="font-normal text-gray-400 line-through">×{{ group.rate_multiplier }}</span>
          <span :data-testid="`catalog-rate-${group.id}`">×{{ group.user_rate_multiplier ?? group.rate_multiplier }}</span>
          <span v-if="hasCustomRate(group)" class="text-[10px]">{{ t('channelStatus.catalog.customRate') }}</span>
        </span>
      </div>
      <div class="mt-2 flex flex-wrap items-center gap-1.5" :aria-label="t('channelStatus.catalog.models')">
        <span v-for="model in group.available_models" :key="`${model.platform}-${model.name}`" class="max-w-full break-all rounded-md bg-white/90 px-2 py-0.5 font-mono text-[11px] leading-5 text-gray-600 ring-1 ring-inset ring-gray-200/70 dark:bg-dark-800/70 dark:text-gray-300 dark:ring-dark-600/70" :title="model.platform">{{ model.name }}</span>
        <span v-if="!group.available_models.length" class="text-xs text-gray-400">{{ t('channelStatus.catalog.noModels') }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { ChannelCatalogGroup } from '@/api/channelMonitorCatalog'

const props = defineProps<{ groups: ChannelCatalogGroup[]; monitorName?: string }>()
const { t } = useI18n()

function showGroupName(group: ChannelCatalogGroup): boolean {
  if (props.groups.length > 1 || !props.monitorName) return true
  const normalizeName = (name: string) => name.replace(/\s+/g, '').toLocaleLowerCase()
  return normalizeName(group.name) !== normalizeName(props.monitorName)
}

function hasCustomRate(group: ChannelCatalogGroup): boolean {
  return group.user_rate_multiplier != null && group.user_rate_multiplier !== group.rate_multiplier
}
</script>
