import type { RouteRecordRaw } from 'vue-router'

export const accountCustomizationRoutes: RouteRecordRaw[] = [
  {
    path: '/admin/openai-daily-quota',
    name: 'AdminOpenAIDailyQuota',
    component: () => import('@/views/admin/OpenAIDailyQuotaView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'OpenAI Daily Quota',
      titleKey: 'admin.openaiDailyQuota.title',
      descriptionKey: 'admin.openaiDailyQuota.description'
    }
  },
  {
    path: '/admin/account-evaluation',
    name: 'AdminAccountEvaluation',
    component: () => import('@/views/admin/AccountEvaluationView.vue'),
    meta: {
      requiresAuth: true,
      requiresAdmin: true,
      title: 'Account Evaluation',
      titleKey: 'admin.accounts.evaluation.title',
      descriptionKey: 'admin.accounts.evaluation.description'
    }
  }
]
