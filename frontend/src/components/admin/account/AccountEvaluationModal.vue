<template>
  <BaseDialog :show="show" :title="t('admin.accounts.evaluation.title')" width="wide" @close="close">
    <div class="space-y-4">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ t('admin.accounts.evaluation.accounts', { count: runnableAccounts.length }) }}
        <span class="ml-1">{{ runnableAccounts.map(account => account.name).join('、') }}</span>
      </p>
      <p v-if="skipped" class="text-sm text-amber-700 dark:text-amber-300">
        {{ t('admin.accounts.evaluation.skipped', { count: skipped }) }}
      </p>
      <p v-for="warning in historyWarnings" :key="warning.id" role="status" class="text-sm text-amber-700 dark:text-amber-300">
        {{ warning.name }}: {{ t(warning.key) }}
      </p>
      <div class="grid max-w-xl gap-3 sm:grid-cols-2">
        <label class="space-y-1 text-sm">
          <span>{{ t('admin.accounts.evaluation.kind') }}</span>
          <select v-model="kind" class="input" :disabled="running || settingsSaving" data-testid="evaluation-kind">
            <option value="candy">{{ t('admin.accounts.evaluation.candy') }}</option>
            <option value="pelican">{{ t('admin.accounts.evaluation.pelican') }}</option>
          </select>
        </label>
        <label class="space-y-1 text-sm">
          <span>{{ t('admin.accounts.evaluation.runs') }}</span>
          <input v-model.number="runs" type="number" min="1" max="10" step="1" class="input" :disabled="running || settingsSaving" data-testid="evaluation-runs" />
        </label>
      </div>
      <AccountEvaluationOptions :models="modelOptions" :disabled="running" />
      <details class="text-xs text-gray-500 dark:text-gray-400">
        <summary class="cursor-pointer">{{ t(`admin.accounts.evaluation.${kind}Hint`) }}</summary>
        <p class="mt-2">{{ t('admin.accounts.evaluation.usageHint') }}</p>
      </details>

      <div v-if="running" role="status" class="space-y-2 text-sm text-primary-600 dark:text-primary-300">
        <div v-for="(active, id) in activeTests" :key="id">
          {{ t('admin.accounts.evaluation.progress', { name: active.name, run: active.run, runs }) }}
          <pre v-if="liveAnswers[Number(id)]" class="mt-1 max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-950 p-3 text-xs text-gray-200">{{ liveAnswers[Number(id)] }}</pre>
        </div>
      </div>
      <p v-else-if="stopped" role="status" class="text-sm text-gray-500">{{ t('admin.accounts.evaluation.stopped') }}</p>
      <p v-for="(error, index) in errors" :key="index" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>

      <div v-if="results.length" class="space-y-3">
        <h4 class="font-medium">{{ t('admin.accounts.evaluation.results') }}</h4>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluation.historyLimit') }}</p>
        <article v-for="row in results" :key="`${row.accountID}-${row.result.id}`" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <strong>{{ row.accountName }}</strong>
            <span>{{ t(`admin.accounts.evaluation.${row.result.kind}`) }}</span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.accounts.evaluation.source.${row.result.source ?? 'manual'}`) }}</span>
            <span :class="statusClass(row.result.status)" class="rounded px-2 py-0.5 text-xs font-medium">
              {{ t(`admin.accounts.evaluation.status.${row.result.status}`) }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">{{ row.result.model }} · {{ row.result.effort }} · {{ (row.result.duration_ms / 1000).toFixed(1) }}s</span>
            <time class="text-xs text-gray-500 dark:text-gray-400">{{ formatTime(row.result.tested_at) }}</time>
          </div>
          <p v-if="row.result.scheduling_stopped" class="mt-2 text-sm font-medium text-red-600 dark:text-red-400">{{ t('admin.accounts.evaluation.schedulingStoppedByTest') }}</p>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.evaluation.proxy') }}: {{ row.result.proxy_name || (row.result.proxy_id ? `#${row.result.proxy_id}` : t(row.result.proxy_id === null ? 'admin.accounts.evaluation.directConnection' : 'admin.accounts.evaluation.proxyNotRecorded')) }}
          </p>
          <p v-if="row.result.error" class="mt-2 whitespace-pre-wrap text-sm text-red-600 dark:text-red-400">{{ row.result.error }}</p>
          <div v-if="row.result.kind === 'pelican' && row.result.svg" class="mt-3">
            <EvaluationSvgPreview :svg="row.result.svg" :alt="t('admin.accounts.evaluation.pelican')" />
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.accounts.evaluation.visualReview') }}</p>
          </div>
          <details v-if="row.result.answer" class="mt-2 text-sm" :open="row.result.kind === 'candy'">
            <summary class="cursor-pointer text-primary-600 dark:text-primary-300">{{ t('admin.accounts.evaluation.answer') }}</summary>
            <pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-gray-50 p-2 text-xs dark:bg-dark-700">{{ row.result.answer }}</pre>
          </details>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.evaluation.tokens', { input: row.result.input_tokens ?? 0, output: row.result.output_tokens ?? 0, reasoning: row.result.reasoning_tokens ?? 0 }) }}
          </p>
        </article>
      </div>
      <p v-else-if="!running" class="py-6 text-center text-sm text-gray-500">{{ t('admin.accounts.evaluation.empty') }}</p>
    </div>
    <template #footer>
      <button class="btn btn-secondary" @click="close">{{ t('common.close') }}</button>
      <button v-if="running" class="btn btn-secondary" @click="stop" data-testid="evaluation-stop">{{ t('admin.accounts.evaluation.stop') }}</button>
      <button v-else class="btn btn-primary" :disabled="!canStart" @click="start" data-testid="evaluation-start">{{ t('admin.accounts.startTest') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import EvaluationSvgPreview from './EvaluationSvgPreview.vue'
import AccountEvaluationOptions from './AccountEvaluationOptions.vue'
import { adminAPI } from '@/api/admin'
import {
  evaluateAccount, isEvaluationResult, supportsAccountEvaluation,
  type AccountEvaluationResult, type EvaluationKind
} from '@/api/admin/accountEvaluation'
import { extractApiErrorMessage } from '@/utils/apiError'
import { concurrentMap } from '@/utils/concurrentMap'
import { resolveAccountEvaluationModel, useAccountEvaluationSettings } from '@/composables/useAccountEvaluationModel'
import type { AccountEvaluationTarget } from '@/api/admin/accountEvaluationHistory'
import type { ClaudeModel } from '@/types'

const props = withDefaults(defineProps<{ show: boolean; accounts: AccountEvaluationTarget[]; skipped?: number }>(), { skipped: 0 })
const emit = defineEmits<{ close: []; updated: [] }>()
const { t } = useI18n()
const kind = ref<EvaluationKind>('candy')
const runs = ref(1)
const { model, effort, loaded: settingsLoaded, saving: settingsSaving, optionsDirty, saveOptions } = useAccountEvaluationSettings()
const modelOptions = ref<ClaudeModel[]>([])
const running = ref(false)
const stopped = ref(false)
const activeTests = ref<Record<number, { name: string; run: number }>>({})
const liveAnswers = ref<Record<number, string>>({})
const pendingAnswers = new Map<number, { chunks: string[]; length: number }>()
let flushTimer: ReturnType<typeof setTimeout> | null = null
const errors = ref<string[]>([])
type ResultRow = { accountID: number; accountName: string; result: AccountEvaluationResult }
const results = ref<ResultRow[]>([])
let controller: AbortController | null = null
let loadVersion = 0
const runnableAccounts = computed(() => props.accounts.filter(account => account.available !== false && supportsAccountEvaluation(account)))
const historyWarnings = computed(() => props.accounts.flatMap(account => {
  if (account.available === false || !supportsAccountEvaluation(account)) {
    return [{ id: account.id, name: account.name, key: 'admin.accounts.evaluation.historyAccountUnavailable' }]
  }
  if (account.history_error) {
    return [{ id: account.id, name: account.name, key: 'admin.accounts.evaluation.historyUnavailable' }]
  }
  return []
}))
const canStart = computed(() => settingsLoaded.value && !settingsSaving.value && runnableAccounts.value.length > 0 &&
  Number.isInteger(runs.value) && runs.value >= 1 && runs.value <= 10)

function statusClass(status: AccountEvaluationResult['status']) {
  if (status === 'correct') return 'bg-green-100 text-green-800 dark:bg-green-900/40 dark:text-green-300'
  if (status === 'wrong') return 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300'
  if (status === 'failed') return 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-300'
  return 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300'
}

function formatTime(value: string) {
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '' : time.toLocaleString()
}

function stop() {
  controller?.abort()
  controller = null
  clearStreaming()
  running.value = false
  stopped.value = true
}

function clearStreaming() {
  if (flushTimer !== null) clearTimeout(flushTimer)
  flushTimer = null
  pendingAnswers.clear()
  liveAnswers.value = {}
  activeTests.value = {}
}

function appendContent(id: number, text: string) {
  const limit = 16000
  const pending = pendingAnswers.get(id) ?? { chunks: [], length: 0 }
  if (text.length >= limit) {
    pending.chunks = [text.slice(-limit)]
    pending.length = limit
  } else {
    pending.chunks.push(text)
    pending.length += text.length
    while (pending.length > limit) {
      const excess = pending.length - limit
      if (pending.chunks[0].length <= excess) {
        pending.length -= pending.chunks.shift()!.length
      } else {
        pending.chunks[0] = pending.chunks[0].slice(excess)
        pending.length -= excess
      }
    }
  }
  pendingAnswers.set(id, pending)
  if (flushTimer !== null) return
  // Join once per paint interval instead of copying a 16K accumulated string per token.
  flushTimer = setTimeout(() => {
    for (const [accountID, answer] of pendingAnswers) {
      if (activeTests.value[accountID]) {
        liveAnswers.value[accountID] = ((liveAnswers.value[accountID] ?? '') + answer.chunks.join('')).slice(-limit)
      }
    }
    pendingAnswers.clear()
    flushTimer = null
  }, 100)
}

function close() {
  loadVersion++
  stop()
  emit('close')
}

watch(() => props.show, async (show) => {
  const version = ++loadVersion
  stop()
  if (!show) return
  stopped.value = false
  modelOptions.value = []
  errors.value = []
  results.value = props.accounts.flatMap(account => {
    const history = account.results
    return Array.isArray(history)
      ? history.filter(isEvaluationResult).slice(0, 5).map(result => ({ accountID: account.id, accountName: account.name, result }))
      : []
  }).sort((a, b) => b.result.tested_at.localeCompare(a.result.tested_at))
  if (runnableAccounts.value[0]) {
    try {
      const options = await adminAPI.accounts.getAvailableModels(runnableAccounts.value[0].id)
      if (version === loadVersion && props.show) modelOptions.value = options
    } catch {
      // The configured model and built-in default remain available in the select.
    }
  }
}, { immediate: true })

async function start() {
  if (!canStart.value || running.value) return
  const version = loadVersion
  if (optionsDirty.value && !(await saveOptions())) return
  if (!props.show || version !== loadVersion) return
  const current = new AbortController()
  controller = current
  running.value = true
  stopped.value = false
  errors.value = []
  clearStreaming()
  const request = { kind: kind.value, model_id: resolveAccountEvaluationModel(model.value), effort: effort.value, runs: runs.value }
  let updated = false
  try {
    // A fixed worker pool improves batch throughput without opening 20 streams.
    await concurrentMap([...runnableAccounts.value], 3, async account => {
      activeTests.value[account.id] = { name: account.name, run: 1 }
      try {
        await evaluateAccount(account.id, request, event => {
          if (current.signal.aborted) return
          if (event.type === 'test_start') {
            activeTests.value[account.id] = { name: account.name, run: event.data?.run ?? 1 }
            pendingAnswers.delete(account.id)
            delete liveAnswers.value[account.id]
          } else if (event.type === 'content' && event.text) {
            appendContent(account.id, event.text)
          } else if (event.type === 'evaluation_result' && isEvaluationResult(event.data)) {
            results.value.unshift({ accountID: account.id, accountName: account.name, result: event.data })
            let kept = 0
            results.value = results.value.filter(row => row.accountID !== account.id || ++kept <= 5)
            updated = true
          } else if (event.type === 'status' && event.code === 'evaluation_history_save_failed') {
            errors.value.push(`${account.name}: ${t('admin.accounts.evaluation.historySaveFailed')}`)
          }
        }, current.signal)
      } catch (error) {
        if (current.signal.aborted) return
        errors.value.push(`${account.name}: ${extractApiErrorMessage(error, t('admin.accounts.evaluation.requestFailed'))}`)
      } finally {
        if (controller === current) {
          delete activeTests.value[account.id]
          delete liveAnswers.value[account.id]
          pendingAnswers.delete(account.id)
        }
      }
    }, current.signal)
  } catch (error) {
    if (!current.signal.aborted) errors.value.push(extractApiErrorMessage(error, t('admin.accounts.evaluation.requestFailed')))
  } finally {
    if (controller === current) {
      controller = null
      running.value = false
      clearStreaming()
    }
    if (updated) emit('updated')
  }
}

onUnmounted(() => {
  loadVersion++
  stop()
})
</script>
