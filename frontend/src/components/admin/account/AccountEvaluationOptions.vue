<template>
  <div class="space-y-2" data-testid="evaluation-options">
    <div class="flex flex-wrap items-end gap-3">
      <label class="min-w-0 w-full space-y-1 text-xs text-gray-500 dark:text-gray-400 sm:w-auto sm:max-w-xs sm:flex-1">
        <span>{{ t('admin.accounts.testModel') }}</span>
        <select v-model="model" class="input w-full" :disabled="disabled || loading || saving || !loaded" data-testid="evaluation-model">
          <option v-for="option in options" :key="option.id" :value="option.id">{{ option.display_name }}</option>
        </select>
      </label>
      <label class="min-w-0 flex-1 space-y-1 text-xs text-gray-500 dark:text-gray-400 sm:w-36 sm:flex-none">
        <span>{{ t('admin.accounts.evaluation.effort') }}</span>
        <select v-model="effort" class="input w-full" :disabled="disabled || loading || saving || !loaded" data-testid="evaluation-effort">
          <option v-for="value in ACCOUNT_EVALUATION_EFFORTS" :key="value" :value="value">{{ value }}</option>
        </select>
      </label>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="disabled || !loaded || saving || !optionsValid || !optionsDirty" data-testid="evaluation-save-options" @click="saveOptions">
        {{ saving ? t('common.saving') : t('common.save') }}
      </button>
      <span v-if="optionsSaved" role="status" class="self-center text-xs text-emerald-600 dark:text-emerald-400">{{ t('admin.accounts.evaluation.settingsSaved') }}</span>
    </div>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluation.sharedOptions') }}</p>
    <div v-if="error" class="flex items-center gap-3 text-xs text-red-600 dark:text-red-400" role="alert">
      <span>{{ errorMessage }}</span>
      <button v-if="!loaded" type="button" class="underline" :disabled="loading" data-testid="evaluation-settings-retry" @click="load">{{ t('admin.accounts.retry') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { ACCOUNT_EVALUATION_EFFORTS, accountEvaluationModelOptions, useAccountEvaluationSettings } from '@/composables/useAccountEvaluationModel'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { ClaudeModel } from '@/types'

const props = withDefaults(defineProps<{ models: ClaudeModel[]; disabled?: boolean }>(), { disabled: false })
const { t } = useI18n()
const { model, effort, loading, loaded, saving, optionsSaved, error, optionsValid, optionsDirty, load, saveOptions } = useAccountEvaluationSettings()
const options = computed(() => accountEvaluationModelOptions(props.models, model.value))
const errorMessage = computed(() => extractApiErrorMessage(error.value, t(loaded.value ? 'admin.accounts.evaluation.automation.saveFailed' : 'admin.accounts.evaluation.automation.loadFailed')))
onMounted(load)
</script>
