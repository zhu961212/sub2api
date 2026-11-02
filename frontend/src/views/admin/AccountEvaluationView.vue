<template>
  <AppLayout>
    <div class="space-y-4">
      <header>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.evaluation.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluation.description') }}</p>
      </header>

      <section class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <div class="inline-flex gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-900" role="group" :aria-label="t('admin.accounts.evaluation.kind')">
            <button v-for="option in kinds" :key="option" type="button" class="rounded-md px-4 py-2 text-sm font-medium transition-colors disabled:opacity-60" :class="kind === option ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-700 dark:text-white' : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'" :aria-pressed="kind === option" :disabled="running || settingsSaving" :data-testid="`kind-${option}`" @click="kind = option">
              {{ t(`admin.accounts.evaluation.${option}`) }}
            </button>
          </div>
          <button v-if="running" type="button" class="btn btn-secondary btn-sm" data-testid="evaluation-stop" @click="stop">{{ t('admin.accounts.evaluation.stop') }}</button>
          <button v-else type="button" class="btn btn-primary btn-sm" :disabled="loading || !settingsLoaded || settingsSaving || !batchTargets.length" data-testid="test-batch" @click="start(batchTargets)">
            <Icon name="play" size="sm" class="mr-1.5" aria-hidden="true" />
            {{ t(selected.size ? 'admin.accounts.evaluation.testSelected' : 'admin.accounts.evaluation.testPage', { count: batchTargets.length }) }}
          </button>
        </div>
        <div class="space-y-3 px-4 py-3">
          <AccountEvaluationOptions :models="availableModels" :disabled="running" />
          <details class="text-xs text-gray-500 dark:text-gray-400">
            <summary class="cursor-pointer">{{ t(`admin.accounts.evaluation.${kind}Hint`) }}</summary>
            <p class="mt-2">{{ t('admin.accounts.evaluation.batchHint') }}</p>
            <p class="mt-1">{{ t('admin.accounts.evaluation.usageHint') }}</p>
          </details>
        </div>
      </section>

      <div class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
        <form class="flex flex-wrap items-center gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700" @submit.prevent="searchAccounts">
          <div class="relative min-w-0 flex-1 sm:max-w-xs">
            <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" aria-hidden="true" />
            <input v-model="search" class="input w-full pl-9" :disabled="running" :placeholder="t('admin.accounts.evaluation.search')" :aria-label="t('admin.accounts.evaluation.search')" />
          </div>
          <button class="btn btn-secondary btn-sm" type="submit" :disabled="loading || running">{{ t('common.search') }}</button>
          <button class="btn btn-secondary btn-sm !px-2" type="button" :disabled="loading || running" :aria-label="t('common.refresh')" :title="t('common.refresh')" data-testid="refresh-accounts" @click="load">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" aria-hidden="true" />
          </button>
          <div class="ml-auto flex items-center gap-3 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ t('admin.accounts.evaluation.accountTotal', { count: total }) }}</span>
            <button v-if="selected.size" type="button" class="text-primary-600 hover:underline dark:text-primary-400" :disabled="running" @click="selected.clear()">{{ t('admin.accounts.bulkActions.clear') }}</button>
          </div>
        </form>
        <p v-if="error" role="alert" class="border-b border-gray-100 px-4 py-3 text-sm text-red-600 dark:border-dark-700 dark:text-red-400">{{ error }}</p>
        <p v-if="running || stopped" role="status" aria-live="polite" class="border-b border-gray-100 px-4 py-2 text-xs text-primary-600 dark:border-dark-700 dark:text-primary-400" data-testid="batch-progress">
          {{ running ? t('admin.accounts.evaluation.batchProgress', { done: finishedCount, count: batchCount }) : t('admin.accounts.evaluation.stopped') }}
        </p>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[640px] table-fixed text-left text-sm">
            <thead class="border-b border-gray-100 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
              <tr>
                <th scope="col" class="w-[38%] px-4 py-3 font-medium">
                  <div class="flex items-center gap-4">
                    <input type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" :checked="allSelected" :indeterminate="someSelected && !allSelected" :disabled="loading || running || !accounts.length" :aria-label="t('admin.accounts.bulkActions.selectCurrentPage')" @change="togglePage" />
                    {{ t('admin.accounts.account') }}
                  </div>
                </th>
                <th scope="col" class="w-24 px-3 py-3 font-medium">{{ t('admin.accounts.evaluation.scheduling') }}</th>
                <th scope="col" class="px-4 py-3 font-medium">{{ t(kind === 'pelican' ? 'admin.accounts.evaluation.drawingResults' : 'admin.accounts.evaluation.latest') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="loading"><td colspan="3" class="p-10 text-center text-gray-500" role="status">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="!accounts.length"><td colspan="3" class="p-10 text-center text-gray-500">{{ t('admin.accounts.evaluation.noAccounts') }}</td></tr>
              <template v-for="account in loading ? [] : accounts" :key="account.id">
                <tr :data-testid="`account-row-${account.id}`" class="transition-colors hover:bg-gray-50/70 dark:hover:bg-dark-700/30">
                  <td class="px-4 py-4">
                    <div class="flex items-center gap-4">
                      <input type="checkbox" class="h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600" :checked="selected.has(account.id)" :disabled="running" :aria-label="account.name" @change="toggle(account)" />
                      <button type="button" class="flex min-w-0 items-center gap-2 text-left" :aria-expanded="expanded.has(account.id)" :aria-controls="`evaluation-history-${account.id}`" :data-testid="`history-${account.id}`" @click="toggleHistory(account)">
                        <Icon :name="expanded.has(account.id) ? 'chevronDown' : 'chevronRight'" size="xs" class="shrink-0 text-gray-400" aria-hidden="true" />
                        <span class="min-w-0">
                          <span class="block truncate font-medium text-gray-900 dark:text-gray-100" :title="account.name">{{ account.name }}</span>
                          <span class="mt-1 flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
                            <span class="rounded border border-gray-200 px-1.5 leading-4 dark:border-dark-600">OAuth</span>
                            <span v-if="planLabel(account)" class="truncate rounded bg-gray-100 px-1.5 leading-4 dark:bg-dark-700">{{ planLabel(account) }}</span>
                          </span>
                        </span>
                      </button>
                    </div>
                  </td>
                  <td class="px-3 py-4">
                    <button type="button" role="switch" :aria-checked="account.schedulable" :aria-label="`${account.name} · ${t('admin.accounts.evaluation.scheduling')}`" :title="t(account.schedulable ? 'admin.accounts.evaluation.schedulingEnabled' : 'admin.accounts.evaluation.schedulingStopped')" :disabled="scheduling.has(account.id)" class="relative inline-flex h-5 w-9 shrink-0 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:opacity-50 dark:focus:ring-offset-dark-800" :class="account.schedulable ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-600'" @click="toggleScheduling(account)">
                      <span class="pointer-events-none h-4 w-4 rounded-full bg-white shadow-sm transition-transform" :class="account.schedulable ? 'translate-x-4' : 'translate-x-0'" />
                    </button>
                  </td>
                  <td class="px-4 py-4">
                    <div class="flex items-center justify-between gap-3">
                      <div class="min-w-0">
                        <span v-if="activeAccounts.has(account.id) || queuedAccounts.has(account.id)" class="inline-flex items-center gap-1.5 text-xs text-primary-600 dark:text-primary-400" role="status">
                          <span class="h-3 w-3 animate-spin rounded-full border-2 border-current border-r-transparent" aria-hidden="true" />
                          {{ t(activeAccounts.has(account.id) ? 'admin.accounts.evaluation.testing' : 'admin.accounts.evaluation.queued') }}
                        </span>
                        <template v-else-if="currentRowErrors[account.id]">
                          <span class="text-xs text-red-600 dark:text-red-400">{{ t('admin.accounts.evaluation.status.failed') }}</span>
                          <p class="mt-1 break-words text-xs text-red-600 dark:text-red-400" role="alert">{{ currentRowErrors[account.id] }}</p>
                        </template>
                        <template v-else-if="latestResults[account.id]">
                          <button type="button" class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium" :class="statusClass(latestResults[account.id].status)" @click="toggleHistory(account)">
                            <Icon :name="latestResults[account.id].status === 'correct' || latestResults[account.id].status === 'completed' ? 'checkCircle' : 'xCircle'" size="xs" aria-hidden="true" />
                            {{ t(`admin.accounts.evaluation.status.${latestResults[account.id].status}`) }}
                          </button>
                          <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400" :title="latestResults[account.id].model">
                            {{ t(`admin.accounts.evaluation.${latestResults[account.id].kind}`) }} · {{ (latestResults[account.id].duration_ms / 1000).toFixed(1) }}s
                          </p>
                          <time class="mt-0.5 block text-[11px] text-gray-400" :datetime="latestResults[account.id].tested_at">{{ formatTime(latestResults[account.id].tested_at) }}</time>
                        </template>
                        <span v-else class="text-xs text-gray-400">{{ t('admin.accounts.evaluation.notTested') }}</span>
                        <p v-if="currentRowWarnings[account.id]" class="mt-1 text-xs text-amber-600 dark:text-amber-400" role="status">{{ currentRowWarnings[account.id] }}</p>
                      </div>
                      <button type="button" class="btn btn-secondary btn-sm shrink-0" :disabled="!settingsLoaded || settingsSaving || activeAccounts.has(account.id) || queuedAccounts.has(account.id)" :data-testid="`test-account-${account.id}`" @click="start([account])">
                        <Icon name="play" size="sm" class="mr-1" aria-hidden="true" />{{ t('admin.accounts.evaluation.test') }}
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="expanded.has(account.id)" :id="`evaluation-history-${account.id}`">
                  <td colspan="3" class="bg-gray-50/70 px-5 py-4 dark:bg-dark-900/40 sm:px-12">
                    <div class="mb-3 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
                      <span>{{ t(kind === 'pelican' ? 'admin.accounts.evaluation.drawingResults' : 'admin.accounts.evaluation.results') }}</span>
                      <span>{{ t('admin.accounts.evaluation.historyLimit') }}</span>
                    </div>
                    <p v-if="historyLoading.has(account.id)" class="text-sm text-gray-500" role="status">{{ t('common.loading') }}</p>
                    <div v-if="currentHistoryErrors[account.id]" class="mb-3 flex items-center gap-3 text-xs text-amber-700 dark:text-amber-400" role="status">
                      <span>{{ currentHistoryErrors[account.id] }}</span>
                      <button type="button" class="underline" :disabled="historyLoading.has(account.id)" @click="loadHistory(account)">{{ t('admin.accounts.retry') }}</button>
                    </div>
                    <div v-if="resultsFor(account.id).length" class="space-y-3">
                      <article v-for="result in resultsFor(account.id)" :key="result.id" class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800">
                        <div class="flex flex-wrap items-center gap-2 text-xs">
                          <span class="rounded-md px-2 py-0.5 font-medium" :class="statusClass(result.status)">{{ t(`admin.accounts.evaluation.status.${result.status}`) }}</span>
                          <span>{{ t(`admin.accounts.evaluation.${result.kind}`) }}</span>
                          <span class="text-gray-500 dark:text-gray-400">{{ result.model }} · {{ result.effort }} · {{ (result.duration_ms / 1000).toFixed(1) }}s</span>
                          <time class="text-gray-400" :datetime="result.tested_at">{{ formatTime(result.tested_at) }}</time>
                          <span class="text-gray-400">{{ t(`admin.accounts.evaluation.source.${result.source ?? 'manual'}`) }}</span>
                        </div>
                        <p v-if="result.error" class="mt-2 whitespace-pre-wrap break-words text-sm text-red-600 dark:text-red-400">{{ result.error }}</p>
                        <p v-if="result.scheduling_stopped" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ t('admin.accounts.evaluation.schedulingStoppedByTest') }}</p>
                        <p v-if="result.scheduling_resumed" class="mt-2 text-xs text-emerald-600 dark:text-emerald-400">{{ t('admin.accounts.evaluation.schedulingResumedByTest') }}</p>
                        <div v-if="result.kind === 'pelican' && result.svg" class="mt-3">
                          <EvaluationSvgPreview :svg="result.svg" :alt="t('admin.accounts.evaluation.pelican')" />
                          <p class="mt-1 text-xs text-gray-500">{{ t('admin.accounts.evaluation.visualReview') }}</p>
                        </div>
                        <details v-if="result.answer" class="mt-2 text-xs">
                          <summary class="cursor-pointer text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white">{{ t('admin.accounts.evaluation.answer') }}</summary>
                          <pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 dark:bg-dark-900">{{ result.answer }}</pre>
                        </details>
                      </article>
                    </div>
                    <p v-else-if="!historyLoading.has(account.id)" class="text-sm text-gray-400">{{ t(kind === 'pelican' ? 'admin.accounts.evaluation.pelicanEmpty' : 'admin.accounts.evaluation.notTested') }}</p>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>
        </div>
        <Pagination v-if="total > 0" :page="page" :page-size="pageSize" :total="total" :page-size-options="[20, 50, 100]" @update:page="changePage" @update:page-size="changePageSize" />
      </div>
      <AccountEvaluationAutomationSettings />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountEvaluationAutomationSettings from '@/components/admin/account/AccountEvaluationAutomationSettings.vue'
import AccountEvaluationOptions from '@/components/admin/account/AccountEvaluationOptions.vue'
import EvaluationSvgPreview from '@/components/admin/account/EvaluationSvgPreview.vue'
import { adminAPI } from '@/api/admin'
import { evaluateAccount, isEvaluationResult, type AccountEvaluationRequest, type AccountEvaluationResult, type EvaluationKind } from '@/api/admin/accountEvaluation'
import { getAccountEvaluationHistory } from '@/api/admin/accountEvaluationHistory'
import { extractApiErrorMessage } from '@/utils/apiError'
import { openAIPlanTypeLabel } from '@/utils/planType'
import { concurrentMap } from '@/utils/concurrentMap'
import { resolveAccountEvaluationModel, useAccountEvaluationSettings } from '@/composables/useAccountEvaluationModel'
import type { AccountListItem, ClaudeModel } from '@/types'

const { t } = useI18n()
const kinds: EvaluationKind[] = ['candy', 'pelican']
const kind = ref<EvaluationKind>('candy')
const { model, effort, loaded: settingsLoaded, saving: settingsSaving, optionsDirty, saveOptions } = useAccountEvaluationSettings()
const availableModels = ref<ClaudeModel[]>([])
const accounts = ref<AccountListItem[]>([])
const selected = ref(new Map<number, AccountListItem>())
const search = ref('')
const appliedSearch = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const running = ref(false)
const stopped = ref(false)
const finishedCount = ref(0)
const batchCount = ref(0)
const error = ref('')
const activeAccounts = ref(new Set<number>())
const queuedAccounts = ref(new Set<number>())
const scheduling = ref(new Set<number>())
const expandedByKind = ref<Record<EvaluationKind, Set<number>>>({ candy: new Set(), pelican: new Set() })
const expanded = computed(() => expandedByKind.value[kind.value])
const historyLoading = ref(new Set<number>())
const histories = ref<Record<number, AccountEvaluationResult[]>>({})
const sessionResults = ref<Record<number, AccountEvaluationResult[]>>({})
const historyErrors = ref<Record<EvaluationKind, Record<number, string>>>({ candy: {}, pelican: {} })
const rowErrors = ref<Record<EvaluationKind, Record<number, string>>>({ candy: {}, pelican: {} })
const rowWarnings = ref<Record<EvaluationKind, Record<number, string>>>({ candy: {}, pelican: {} })
const currentHistoryErrors = computed(() => historyErrors.value[kind.value])
const currentRowErrors = computed(() => rowErrors.value[kind.value])
const currentRowWarnings = computed(() => rowWarnings.value[kind.value])
const historyRequests = new Map<number, AbortController>()
let request: AbortController | null = null
let evaluationRequest: AbortController | null = null
type EvaluationJob = { account: AccountListItem; request: AccountEvaluationRequest }
const evaluationQueue: EvaluationJob[] = []
let disposed = false
const allSelected = computed(() => accounts.value.length > 0 && accounts.value.every(account => selected.value.has(account.id)))
const someSelected = computed(() => accounts.value.some(account => selected.value.has(account.id)))
const batchTargets = computed(() => selected.value.size ? [...selected.value.values()] : accounts.value.slice(0, 20))

const latestResults = computed(() => {
  const latest: Record<number, AccountEvaluationResult> = {}
  for (const account of accounts.value) {
    const summary = account.extra?.model_evaluation_results
    const candidates = [...resultsFor(account.id), ...(Array.isArray(summary) ? summary.filter(isEvaluationResult).filter(result => result.kind === kind.value) : [])]
    candidates.sort((a, b) => b.tested_at.localeCompare(a.tested_at))
    if (candidates[0]) latest[account.id] = candidates[0]
  }
  return latest
})

function resultsFor(id: number): AccountEvaluationResult[] {
  const unique = new Map<string, AccountEvaluationResult>()
  for (const result of [...(sessionResults.value[id] ?? []), ...(histories.value[id] ?? [])]) {
    if (result.kind === kind.value && !unique.has(result.id)) unique.set(result.id, result)
  }
  return [...unique.values()].sort((a, b) => b.tested_at.localeCompare(a.tested_at)).slice(0, 5)
}

function statusClass(status: AccountEvaluationResult['status']) {
  if (status === 'correct') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  if (status === 'wrong') return 'bg-red-50 text-red-600 dark:bg-red-900/30 dark:text-red-300'
  if (status === 'failed') return 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  return 'bg-blue-50 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
}

function formatTime(value: string) {
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? '' : time.toLocaleString()
}

function planLabel(account: AccountListItem) {
  const rawPlan = account.credentials?.plan_type || account.parent_plan_type
  const plan = typeof rawPlan === 'string' ? rawPlan : ''
  return openAIPlanTypeLabel(plan) || plan
}

async function load() {
  request?.abort()
  for (const pending of historyRequests.values()) pending.abort()
  historyRequests.clear()
  historyLoading.value.clear()
  const current = new AbortController()
  request = current
  loading.value = true
  error.value = ''
  try {
    const data = await adminAPI.accounts.list(page.value, pageSize.value, {
      platform: 'openai', type: 'oauth', lite: '1', search: appliedSearch.value, sort_by: 'name', sort_order: 'asc'
    }, { signal: current.signal })
    if (current.signal.aborted) return
    accounts.value = data.items
    total.value = data.total
    void loadModelOptions(data.items[0]?.id, current.signal)
    // Complete SVG answers can be large. Keep their cache on the visible page,
    // retaining only in-session verdicts still visible or explicitly selected.
    histories.value = {}
    historyErrors.value = { candy: {}, pelican: {} }
    const visibleIDs = new Set(data.items.map(account => account.id))
    for (const id of Object.keys(sessionResults.value).map(Number)) {
      if (!visibleIDs.has(id) && !selected.value.has(id)) delete sessionResults.value[id]
    }
    for (const project of kinds) {
      for (const id of expandedByKind.value[project]) {
        if (!visibleIDs.has(id)) expandedByKind.value[project].delete(id)
      }
      for (const id of Object.keys(rowErrors.value[project]).map(Number)) {
        if (!visibleIDs.has(id) && !selected.value.has(id)) delete rowErrors.value[project][id]
      }
      for (const id of Object.keys(rowWarnings.value[project]).map(Number)) {
        if (!visibleIDs.has(id) && !selected.value.has(id)) delete rowWarnings.value[project][id]
      }
    }
    for (const account of data.items) {
      if (selected.value.has(account.id)) selected.value.set(account.id, account)
      if (kind.value === 'pelican') expandedByKind.value.pelican.add(account.id)
    }
    // Drawings need their SVG bodies after a reload. Fetch only visible accounts,
    // in batches of at most twenty and with a bounded request pool.
    void loadHistories(data.items.filter(account => expanded.value.has(account.id)), kind.value)
  } catch (cause) {
    if (!current.signal.aborted) error.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    if (request === current) loading.value = false
  }
}

async function loadModelOptions(id: number | undefined, signal: AbortSignal) {
  if (!id) { availableModels.value = []; return }
  try {
    const options = await adminAPI.accounts.getAvailableModels(id, signal)
    if (!signal.aborted && !disposed) availableModels.value = options
  } catch {
    if (!signal.aborted && !disposed) availableModels.value = []
  }
}

function toggle(account: AccountListItem) {
  if (selected.value.has(account.id)) selected.value.delete(account.id)
  else if (selected.value.size < 20) selected.value.set(account.id, account)
  else error.value = t('admin.accounts.evaluation.batchLimit')
}

function togglePage() {
  if (allSelected.value) accounts.value.forEach(account => selected.value.delete(account.id))
  else {
    for (const account of accounts.value) {
      if (selected.value.has(account.id)) continue
      if (selected.value.size >= 20) { error.value = t('admin.accounts.evaluation.batchLimit'); break }
      selected.value.set(account.id, account)
    }
  }
}

async function toggleScheduling(account: AccountListItem) {
  if (scheduling.value.has(account.id)) return
  scheduling.value.add(account.id)
  error.value = ''
  try {
    const updated = await adminAPI.accounts.setSchedulable(account.id, !account.schedulable)
    if (disposed) return
    const current = accounts.value.find(item => item.id === account.id)
    if (current) current.schedulable = updated.schedulable
    const selection = selected.value.get(account.id)
    if (selection) selection.schedulable = updated.schedulable
  } catch (cause) {
    if (!disposed) error.value = extractApiErrorMessage(cause, t('admin.accounts.failedToToggleSchedulable'))
  } finally {
    scheduling.value.delete(account.id)
  }
}

function toggleHistory(account: AccountListItem) {
  if (expanded.value.has(account.id)) expanded.value.delete(account.id)
  else {
    expanded.value.add(account.id)
    if (!histories.value[account.id]) void loadHistory(account)
  }
}

async function loadHistory(account: AccountListItem) {
  await loadHistories([account], kind.value, true)
}

async function loadHistories(targets: AccountListItem[], targetKind: EvaluationKind, force = false) {
  const missing = targets.filter(account => !historyRequests.has(account.id) && (force || !histories.value[account.id]))
  const batches: { ids: number[]; controller: AbortController }[] = []
  for (let offset = 0; offset < missing.length; offset += 20) {
    const ids = missing.slice(offset, offset + 20).map(account => account.id)
    const controller = new AbortController()
    batches.push({ ids, controller })
    for (const id of ids) {
      historyRequests.set(id, controller)
      historyLoading.value.add(id)
      delete historyErrors.value[targetKind][id]
    }
  }
  await concurrentMap(batches, 3, async ({ ids, controller }) => {
    try {
      if (controller.signal.aborted) return
      const response = await getAccountEvaluationHistory(ids, controller.signal)
      if (controller.signal.aborted) return
      const byID = new Map(response.accounts.map(account => [account.id, account]))
      for (const id of ids) {
        const history = byID.get(id)
        if (!history) {
          historyErrors.value[targetKind][id] = t('admin.accounts.evaluation.historyAccountUnavailable')
          continue
        }
        histories.value[id] = history.results.filter(isEvaluationResult).slice(0, 5)
        if (history.history_error) historyErrors.value[targetKind][id] = t('admin.accounts.evaluation.historyUnavailable')
      }
    } catch {
      if (!controller.signal.aborted) {
        for (const id of ids) historyErrors.value[targetKind][id] = t('admin.accounts.evaluation.historyUnavailable')
      }
    } finally {
      for (const id of ids) {
        if (historyRequests.get(id) === controller) {
          historyRequests.delete(id)
          historyLoading.value.delete(id)
        }
      }
    }
  })
}

watch(kind, value => {
  stopped.value = false
  if (value === 'pelican') {
    for (const account of accounts.value) expandedByKind.value.pelican.add(account.id)
    void loadHistories(accounts.value, value)
  }
})

async function start(targets: AccountListItem[]) {
  if (disposed || !settingsLoaded.value || settingsSaving.value) return
  const pageRequest = request
  const targetKind = kind.value
  if (optionsDirty.value && !(await saveOptions())) return
  if (disposed || request !== pageRequest || kind.value !== targetKind) return
  const pending = [...new Map(targets.map(account => [account.id, account])).values()]
    .filter(account => !activeAccounts.value.has(account.id) && !queuedAccounts.value.has(account.id))
  if (!pending.length || evaluationRequest?.signal.aborted || disposed) return
  if (pending.length + activeAccounts.value.size + queuedAccounts.value.size > 20) {
    error.value = t('admin.accounts.evaluation.batchLimit')
    return
  }
  if (!evaluationRequest) {
    evaluationRequest = new AbortController()
    running.value = true
    stopped.value = false
    finishedCount.value = 0
    batchCount.value = 0
  }
  const testRequest: AccountEvaluationRequest = { kind: kind.value, model_id: resolveAccountEvaluationModel(model.value), effort: effort.value, runs: 1 }
  batchCount.value += pending.length
  for (const account of pending) {
    queuedAccounts.value.add(account.id)
    evaluationQueue.push({ account, request: testRequest })
    delete rowErrors.value[testRequest.kind][account.id]
    delete rowWarnings.value[testRequest.kind][account.id]
  }
  fillEvaluationSlots()
}

function fillEvaluationSlots() {
  const current = evaluationRequest
  if (!current || current.signal.aborted) return
  while (activeAccounts.value.size < 3 && evaluationQueue.length) {
    const job = evaluationQueue.shift()!
    queuedAccounts.value.delete(job.account.id)
    activeAccounts.value.add(job.account.id)
    void runEvaluationJob(job, current)
  }
}

async function runEvaluationJob({ account, request: testRequest }: EvaluationJob, current: AbortController) {
  try {
    await evaluateAccount(account.id, testRequest, event => {
      if (current.signal.aborted) return
      if (event.type === 'evaluation_result' && isEvaluationResult(event.data)) {
        sessionResults.value[account.id] = [event.data, ...(sessionResults.value[account.id] ?? [])].slice(0, 5)
        if (event.data.scheduling_stopped || event.data.scheduling_resumed) {
          const schedulable = event.data.scheduling_resumed === true
          const visible = accounts.value.find(item => item.id === account.id)
          if (visible) visible.schedulable = schedulable
          const selection = selected.value.get(account.id)
          if (selection) selection.schedulable = schedulable
        }
        // Drawings are ready to inspect as soon as the result arrives.
        if (event.data.kind === 'pelican' && event.data.svg) expandedByKind.value.pelican.add(account.id)
      } else if (event.type === 'status' && event.code === 'evaluation_history_save_failed') {
        rowWarnings.value[testRequest.kind][account.id] = t('admin.accounts.evaluation.historySaveFailed')
      }
    }, current.signal)
  } catch (cause) {
    if (!current.signal.aborted) rowErrors.value[testRequest.kind][account.id] = extractApiErrorMessage(cause, t('admin.accounts.evaluation.requestFailed'))
  } finally {
    if (evaluationRequest === current) {
      activeAccounts.value.delete(account.id)
      if (!current.signal.aborted) finishedCount.value++
      fillEvaluationSlots()
      if (!activeAccounts.value.size && !evaluationQueue.length) {
        evaluationRequest = null
        running.value = false
      }
    }
  }
}

function stop() {
  evaluationRequest?.abort()
  evaluationQueue.length = 0
  queuedAccounts.value.clear()
  stopped.value = true
}

function searchAccounts() {
  if (running.value) return
  appliedSearch.value = search.value.trim()
  selected.value.clear()
  page.value = 1
  void load()
}

function changePage(value: number) { if (!running.value) { page.value = value; void load() } }
function changePageSize(value: number) { if (!running.value) { pageSize.value = value; page.value = 1; void load() } }
onMounted(load)
onUnmounted(() => {
  disposed = true
  request?.abort()
  stop()
  for (const pending of historyRequests.values()) pending.abort()
})
</script>
