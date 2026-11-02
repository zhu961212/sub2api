<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0">
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.openaiDailyQuota.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.openaiDailyQuota.description') }}</p>
        </div>
        <button v-if="loaded && !loading && !loadError" type="submit" form="openai-daily-quota-policy" class="btn btn-primary btn-sm ml-auto shrink-0" :disabled="saving" data-testid="save-policy">{{ saving ? t('admin.openaiDailyQuota.saving') : t('admin.openaiDailyQuota.save') }}</button>
      </header>

      <div v-if="loading" class="flex min-h-48 items-center justify-center text-gray-500" role="status">
        {{ t('common.loading') }}
      </div>
      <div v-else-if="loadError" class="space-y-3 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800" role="alert">
        <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="loadPolicy">{{ t('admin.openaiDailyQuota.retry') }}</button>
      </div>

      <form v-else-if="loaded" id="openai-daily-quota-policy" class="space-y-3" novalidate @input="savedMessage = ''" @submit.prevent="savePolicy">
        <fieldset :disabled="saving" class="min-w-0 overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <section>
            <div class="grid items-center gap-4 p-4 sm:grid-cols-2 sm:p-5 lg:grid-cols-[minmax(0,1fr)_15rem_10rem] lg:gap-6">
              <div class="min-w-0 sm:col-span-2 lg:col-span-1">
                <label class="inline-flex cursor-pointer items-center gap-3 text-sm font-semibold text-gray-900 dark:text-white">
                  <span class="relative inline-flex shrink-0">
                    <input v-model="form.enabled" type="checkbox" role="switch" class="peer sr-only" data-testid="policy-enabled" />
                    <span class="h-5 w-9 rounded-full bg-gray-300 transition-colors peer-checked:bg-primary-600 peer-focus-visible:ring-2 peer-focus-visible:ring-primary-500 peer-focus-visible:ring-offset-2 dark:bg-dark-600" aria-hidden="true" />
                    <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4" aria-hidden="true" />
                  </span>
                  {{ t('admin.openaiDailyQuota.enabled') }}
                </label>
                <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.openaiDailyQuota.enabledHint') }}</p>
              </div>
              <div>
                <label for="daily-quota-timezone" class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.openaiDailyQuota.resetTimezone') }}</label>
                <input id="daily-quota-timezone" v-model="form.timezone" class="input w-full" list="daily-quota-timezones" placeholder="Asia/Shanghai" required :title="t('admin.openaiDailyQuota.resetTimezoneHint')" data-testid="reset-timezone" />
                <datalist id="daily-quota-timezones">
                  <option value="Asia/Shanghai" />
                  <option value="UTC" />
                  <option value="America/New_York" />
                  <option value="America/Los_Angeles" />
                  <option value="Europe/London" />
                  <option value="Asia/Tokyo" />
                </datalist>
              </div>
              <div>
                <label for="daily-quota-hour" class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">{{ t('admin.openaiDailyQuota.resetHour') }}</label>
                <select id="daily-quota-hour" v-model.number="form.hour" class="input w-full" data-testid="reset-hour">
                  <option v-for="hour in 24" :key="hour - 1" :value="hour - 1">{{ String(hour - 1).padStart(2, '0') }}:00</option>
                </select>
              </div>
            </div>
            <p v-if="!form.enabled" class="px-4 pb-4 text-xs leading-5 text-amber-700 dark:text-amber-300 sm:px-5">{{ t('admin.openaiDailyQuota.disabledHint') }}</p>
            <p v-else class="px-4 pb-4 text-xs leading-5 text-gray-500 dark:text-gray-400 sm:px-5" data-testid="reset-summary">
              {{ t('admin.openaiDailyQuota.resetSummary', { hour: String(form.hour).padStart(2, '0'), timezone: form.timezone.trim() || '—' }) }}
            </p>
          </section>

          <section class="border-t border-gray-100 dark:border-dark-700">
            <div class="p-4 sm:px-5">
              <div class="flex flex-wrap items-center justify-between gap-2">
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.openaiDailyQuota.planTitle') }}</h2>
                <span class="text-xs text-gray-500 dark:text-gray-400" data-testid="account-count">{{ t('admin.openaiDailyQuota.totalAccounts', { count: accountCount }) }}</span>
              </div>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.openaiDailyQuota.planHint') }}</p>
            </div>
            <div>
              <table class="block w-full text-left text-sm md:table">
                <thead class="hidden bg-gray-50 text-xs font-medium text-gray-500 dark:bg-dark-900/40 dark:text-gray-400 md:table-header-group">
                  <tr>
                    <th scope="col" class="px-5 py-2.5 font-medium">{{ t('admin.openaiDailyQuota.plan') }}</th>
                    <th scope="col" class="px-5 py-2.5 font-medium">{{ t('admin.openaiDailyQuota.accountCount') }}</th>
                    <th scope="col" class="px-5 py-2.5 font-medium">{{ t('admin.openaiDailyQuota.dailyLimit') }}</th>
                    <th scope="col" class="px-5 py-2.5 font-medium">{{ t('admin.openaiDailyQuota.fiveHourLimit') }}</th>
                  </tr>
                </thead>
                <tbody class="block divide-y divide-gray-100 dark:divide-dark-700 md:table-row-group">
                  <tr v-for="plan in plans" :key="plan.plan_type" class="grid grid-cols-2 gap-x-3 gap-y-3 px-4 py-4 md:table-row md:p-0" :data-testid="`plan-row-${plan.plan_type}`">
                    <th scope="row" class="min-w-0 self-center break-words font-medium text-gray-900 dark:text-white md:px-5 md:py-4">{{ planLabel(plan.plan_type) }}</th>
                    <td class="self-center text-right tabular-nums text-gray-600 dark:text-gray-300 md:px-5 md:py-4 md:text-left"><span class="mr-1.5 text-xs text-gray-400 md:hidden">{{ t('admin.openaiDailyQuota.accountCount') }}</span>{{ plan.account_count }}</td>
                    <td class="min-w-0 md:px-5 md:py-4">
                      <label :for="`daily-limit-${plan.plan_type}`" class="mb-1.5 block text-xs leading-5 text-gray-500 dark:text-gray-400 md:sr-only">{{ t('admin.openaiDailyQuota.dailyLimit') }}</label>
                      <input :id="`daily-limit-${plan.plan_type}`" v-model="form.planLimits[plan.plan_type]" type="number" min="0" max="1000000000000" step="any" inputmode="decimal" class="input w-full min-w-0 max-w-56" required :aria-label="`${planLabel(plan.plan_type)} — ${t('admin.openaiDailyQuota.dailyLimit')}`" :data-testid="`plan-limit-${plan.plan_type}`" />
                      <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400" :data-testid="`plan-effective-${plan.plan_type}`">{{ effectiveLimitLabel(plan.plan_type) }}</p>
                    </td>
                    <td class="min-w-0 md:px-5 md:py-4">
                      <label :for="`five-hour-limit-${plan.plan_type}`" class="mb-1.5 block text-xs leading-5 text-gray-500 dark:text-gray-400 md:sr-only">{{ t('admin.openaiDailyQuota.fiveHourLimit') }}</label>
                      <input :id="`five-hour-limit-${plan.plan_type}`" v-model="form.plan5hLimits[plan.plan_type]" type="number" min="0" max="1000000000000" step="any" inputmode="decimal" class="input w-full min-w-0 max-w-56" required :aria-label="`${planLabel(plan.plan_type)} — ${t('admin.openaiDailyQuota.fiveHourLimit')}`" :data-testid="`plan-5h-limit-${plan.plan_type}`" />
                      <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400" :data-testid="`plan-5h-effective-${plan.plan_type}`">{{ effectiveLimitLabel(plan.plan_type, true) }}</p>
                    </td>
                  </tr>
                  <tr v-if="plans.length === 0" class="block md:table-row">
                    <td colspan="4" class="block px-5 py-10 text-center text-gray-500 md:table-cell">{{ t('admin.openaiDailyQuota.emptyPlans') }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div class="space-y-1 border-t border-gray-100 px-5 py-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-gray-400">
              <p>{{ t('admin.openaiDailyQuota.fiveHourHint') }}</p>
              <p>{{ t('admin.openaiDailyQuota.shadowHint') }}</p>
              <p>{{ t('admin.openaiDailyQuota.applyHint') }}</p>
            </div>
          </section>
        </fieldset>
        <p v-if="saveError" class="text-sm text-red-600 dark:text-red-400" role="alert" data-testid="save-error">{{ saveError }}</p>
        <p v-if="savedMessage" class="text-sm text-green-700 dark:text-green-400" role="status" data-testid="save-result">{{ savedMessage }}</p>
      </form>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import { normalizePlanType, openAIPlanTypeLabel } from '@/utils/planType'
import {
  getOpenAIDailyQuota,
  updateOpenAIDailyQuota,
  type OpenAIDailyQuotaPlan,
  type OpenAIDailyQuotaPolicy,
  type OpenAIDailyQuotaResponse,
} from '@/api/admin/openaiDailyQuota'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const loadError = ref('')
const saveError = ref('')
const savedMessage = ref('')
const accountCount = ref(0)
const plans = ref<OpenAIDailyQuotaPlan[]>([])
const form = reactive({
  enabled: false,
  timezone: 'Asia/Shanghai',
  hour: 0,
  planLimits: {} as Record<string, string | number>,
  plan5hLimits: {} as Record<string, string | number>,
})

const legacyPlanAliases = new Map([
  ['pro100', 'prolite'],
  ['pro200', 'pro'],
  ['pro500', 'promax'],
])

function planLabel(plan: string): string {
  const key = normalizePlanType(plan)
  if (key === 'unknown') return t('admin.openaiDailyQuota.unknownPlan')
  return openAIPlanTypeLabel(legacyPlanAliases.get(key) ?? key) || plan
}

function applyResponse(response: OpenAIDailyQuotaResponse): void {
  const policy = response.policy
  form.enabled = policy.enabled
  form.timezone = policy.reset_timezone
  form.hour = policy.reset_hour
  form.planLimits = Object.fromEntries(Object.entries(policy.plan_limits ?? {}).map(([plan, limit]) => [plan, String(limit)]))
  form.plan5hLimits = Object.fromEntries(Object.entries(policy.plan_5h_limits ?? {}).map(([plan, limit]) => [plan, String(limit)]))
  // Counts come from existing OAuth parent accounts. Keep hidden plan limits
  // in the policy so editing visible plans does not reset future allocations.
  const rows = new Map((response.plans ?? []).filter(plan => plan.account_count > 0).map(plan => [plan.plan_type, plan]))
  for (const plan of rows.keys()) {
    if (form.planLimits[plan] === undefined) form.planLimits[plan] = '0'
    if (form.plan5hLimits[plan] === undefined) form.plan5hLimits[plan] = '0'
  }
  plans.value = [...rows.values()]
  accountCount.value = response.account_count
  loaded.value = true
}

function effectiveLimitLabel(plan: string, fiveHour = false): string {
  if (!form.enabled) return t('admin.openaiDailyQuota.off')
  const raw = String((fiveHour ? form.plan5hLimits : form.planLimits)[plan] ?? '').trim()
  const limit = Number(raw)
  if (!raw || !Number.isFinite(limit) || limit < 0) return '—'
  return limit === 0 ? t('admin.openaiDailyQuota.unlimited') : t(fiveHour ? 'admin.openaiDailyQuota.perAccount5h' : 'admin.openaiDailyQuota.perAccount', { amount: limit })
}

function readPolicy(): OpenAIDailyQuotaPolicy | null {
  const timezone = form.timezone.trim()
  try {
    if (!timezone) throw new Error('empty timezone')
    new Intl.DateTimeFormat('en', { timeZone: timezone }).format()
  } catch {
    saveError.value = t('admin.openaiDailyQuota.invalidTimezone')
    return null
  }
  if (!Number.isInteger(form.hour) || form.hour < 0 || form.hour > 23) {
    saveError.value = t('admin.openaiDailyQuota.invalidHour')
    return null
  }
  const planLimits: Record<string, number> = {}
  for (const [plan, raw] of Object.entries(form.planLimits)) {
    const text = String(raw).trim()
    const limit = Number(text)
    if (!text || !Number.isFinite(limit) || limit < 0 || limit > 1e12) {
      saveError.value = t('admin.openaiDailyQuota.invalidPlan', { plan: planLabel(plan) })
      return null
    }
    planLimits[plan] = limit
  }
  const plan5hLimits: Record<string, number> = {}
  for (const [plan, value] of Object.entries(form.plan5hLimits)) {
    const raw = String(value).trim()
    const limit = Number(raw)
    if (!raw || !Number.isFinite(limit) || limit < 0 || limit > 1e12) {
      saveError.value = t('admin.openaiDailyQuota.invalid5hPlan', { plan: planLabel(plan) })
      return null
    }
    plan5hLimits[plan] = limit
  }
  return { enabled: form.enabled, reset_hour: form.hour, reset_timezone: timezone, plan_limits: planLimits, plan_5h_limits: plan5hLimits }
}

async function loadPolicy(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    applyResponse(await getOpenAIDailyQuota())
  } catch {
    loadError.value = t('admin.openaiDailyQuota.loadError')
  } finally {
    loading.value = false
  }
}

async function savePolicy(): Promise<void> {
  if (saving.value || !loaded.value) return
  saveError.value = ''
  savedMessage.value = ''
  const policy = readPolicy()
  if (!policy) return
  saving.value = true
  try {
    const response = await updateOpenAIDailyQuota(policy)
    applyResponse(response)
    savedMessage.value = t('admin.openaiDailyQuota.saved', { count: response.applied_count ?? response.account_count })
    appStore.showSuccess(savedMessage.value)
  } catch (error) {
    const message = error && typeof error === 'object' && 'message' in error ? String(error.message || '') : ''
    saveError.value = message || t('admin.openaiDailyQuota.saveError')
    appStore.showError(saveError.value)
  } finally {
    saving.value = false
  }
}

onMounted(loadPolicy)
</script>
