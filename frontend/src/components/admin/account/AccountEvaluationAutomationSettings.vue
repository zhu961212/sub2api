<template>
  <section class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
    <button
      type="button"
      class="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:hover:bg-dark-700"
      :aria-expanded="expanded"
      :aria-controls="panelID"
      data-testid="auto-toggle"
      @click="expanded = !expanded"
    >
      <span class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.evaluation.automation.title') }}</span>
      <span v-if="loaded" class="rounded-full px-2 py-0.5 text-xs" :class="configuredEnabled ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400' : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'" data-testid="auto-status">
        {{ t(configuredEnabled ? 'common.enabled' : 'common.disabled') }}
      </span>
      <span class="ml-auto text-xs text-gray-500 dark:text-gray-400">{{ t(expanded ? 'common.collapse' : 'common.settings') }}</span>
      <svg class="h-4 w-4 shrink-0 text-gray-400 transition-transform" :class="{ 'rotate-180': expanded }" viewBox="0 0 20 20" fill="none" stroke="currentColor" aria-hidden="true"><path d="m5 7.5 5 5 5-5" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
    </button>
    <p v-if="loading" role="status" class="px-4 pb-3 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <div v-else-if="!loaded" class="space-y-2 px-4 pb-3">
      <p role="alert" class="text-sm text-red-600 dark:text-red-400">{{ errorMessage }}</p>
      <button type="button" class="btn btn-secondary btn-sm" data-testid="auto-retry" @click="load">{{ t('admin.accounts.retry') }}</button>
    </div>
    <form v-if="loaded && expanded" :id="panelID" class="space-y-4 border-t border-gray-100 p-4 dark:border-dark-700" @input="saved = false" @change="saved = false" @submit.prevent="save">
      <fieldset :disabled="saving" class="space-y-4">
        <label class="flex items-center gap-2 text-sm font-medium">
          <input v-model="form.enabled" type="checkbox" class="rounded text-primary-600" data-testid="auto-enabled" />
          {{ t('admin.accounts.evaluation.automation.enabled') }}
        </label>
        <div class="grid max-w-xl gap-3 sm:grid-cols-2">
          <label class="space-y-1 text-sm">
            <span>{{ t('admin.accounts.evaluation.automation.interval') }}</span>
            <input v-model.number="form.interval_hours" type="number" min="1" max="168" step="1" required class="input" data-testid="auto-interval" />
          </label>
          <label class="space-y-1 text-sm">
            <span>{{ t('admin.accounts.evaluation.automation.concurrency') }}</span>
            <input v-model.number="form.concurrency" type="number" min="1" max="10" step="1" required class="input" data-testid="auto-concurrency" />
          </label>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluation.sharedOptions') }}</p>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <details class="max-w-3xl text-xs text-gray-500 dark:text-gray-400">
            <summary class="cursor-pointer">{{ t('common.more') }}</summary>
            <div class="mt-2 space-y-1 leading-relaxed">
              <p>{{ t('admin.accounts.evaluation.automation.description') }}</p>
              <p>{{ t('admin.accounts.evaluation.automation.rule') }}</p>
              <p>{{ t('admin.accounts.evaluation.automation.proxyHint') }}</p>
              <p>{{ t('admin.accounts.evaluation.automation.retention') }}</p>
              <p>{{ t('admin.accounts.evaluation.automation.scheduleHint') }}</p>
            </div>
          </details>
          <button type="submit" class="btn btn-primary btn-sm" :disabled="saving || !valid" data-testid="auto-save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </fieldset>
    </form>
    <p v-if="loaded && error" role="alert" class="px-4 pb-3 text-sm text-red-600 dark:text-red-400">{{ errorMessage }}</p>
    <p v-if="saved" role="status" class="px-4 pb-3 text-sm text-green-700 dark:text-green-400">{{ t('admin.accounts.evaluation.automation.saved') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAccountEvaluationSettings } from '@/composables/useAccountEvaluationModel'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const panelID = `evaluation-automation-${useId()}`
const expanded = ref(false)
const { form, configuredEnabled, loading, loaded, saving, saved, error, valid, load, save } = useAccountEvaluationSettings()
const errorMessage = computed(() => extractApiErrorMessage(error.value, t(loaded.value ? 'admin.accounts.evaluation.automation.saveFailed' : 'admin.accounts.evaluation.automation.loadFailed')))

onMounted(load)
</script>
