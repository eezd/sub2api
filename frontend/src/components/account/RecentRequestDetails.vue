<template>
  <div class="space-y-4 text-sm">
    <div class="flex items-start justify-between gap-4">
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span
            :class="[
              'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-semibold',
              request.kind === 'error'
                ? 'bg-red-100 text-red-700 dark:bg-red-950/60 dark:text-red-300'
                : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-300'
            ]"
          >
            {{ request.kind === 'error' ? t('admin.accounts.recentRequests.error') : t('admin.accounts.recentRequests.success') }}
            <template v-if="request.status_code != null"> · {{ request.status_code }}</template>
          </span>
        </div>
        <p class="mt-2 truncate font-medium text-gray-900 dark:text-white">
          {{ accountName }} <span class="font-mono text-xs text-gray-400">#{{ accountId }}</span>
        </p>
      </div>
    </div>

    <p
      v-if="request.kind === 'error'"
      class="break-words rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-xs leading-5 text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
    >
      {{ request.message || t('admin.accounts.recentRequests.unknownError') }}
    </p>

    <dl class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-2.5 text-xs">
      <template v-for="item in detailItems" :key="item.label">
        <dt class="text-gray-500 dark:text-gray-400">{{ item.label }}</dt>
        <dd class="max-w-[12rem] break-all text-right font-medium text-gray-900 dark:text-gray-100" :class="item.mono ? 'font-mono' : ''">
          {{ item.value }}
        </dd>
      </template>
    </dl>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpsRequestDetail } from '@/api/admin/ops'
import { formatDateTime } from '@/utils/format'
import { formatMultiplier } from '@/utils/formatters'

const props = defineProps<{
  request: OpsRequestDetail
  accountId: number
  accountName: string
}>()

const { t } = useI18n()

const formatDuration = (value: number | null | undefined): string => {
  if (value == null) return '—'
  if (value < 1000) return `${value} ms`
  if (value < 60_000) return `${(value / 1000).toFixed(2).replace(/\.00$/, '')} s`
  const minutes = Math.floor(value / 60_000)
  const seconds = ((value % 60_000) / 1000).toFixed(1).replace(/\.0$/, '')
  return `${minutes}m ${seconds}s`
}

const formatCount = (value: number | null | undefined): string => (
  value == null ? '—' : value.toLocaleString()
)

const formatCost = (value: number | null | undefined): string => (
  value == null ? '—' : `$${value.toFixed(6)}`
)

const detailItems = computed(() => [
  { label: t('admin.accounts.recentRequests.requestId'), value: props.request.request_id || '—', mono: true },
  { label: t('admin.accounts.recentRequests.model'), value: props.request.model || '—', mono: true },
  { label: t('admin.accounts.recentRequests.requestTime'), value: formatDateTime(props.request.created_at), mono: false },
  { label: t('admin.accounts.recentRequests.responseTime'), value: formatDuration(props.request.duration_ms), mono: true },
  { label: t('admin.accounts.recentRequests.firstTokenTime'), value: formatDuration(props.request.first_token_ms), mono: true },
  { label: t('admin.accounts.recentRequests.inputTokens'), value: formatCount(props.request.input_tokens), mono: true },
  { label: t('admin.accounts.recentRequests.outputTokens'), value: formatCount(props.request.output_tokens), mono: true },
  { label: t('admin.accounts.recentRequests.actualCost'), value: formatCost(props.request.actual_cost), mono: true },
  { label: t('admin.accounts.recentRequests.accountCost'), value: formatCost(props.request.account_cost), mono: true },
  {
    label: t('admin.accounts.recentRequests.accountMultiplier'),
    value: props.request.account_rate_multiplier == null ? '—' : `${formatMultiplier(props.request.account_rate_multiplier)}x`,
    mono: true
  },
  { label: t('admin.accounts.recentRequests.statusCode'), value: props.request.status_code == null ? '—' : String(props.request.status_code), mono: true },
  {
    label: t('admin.accounts.recentRequests.reason'),
    value: props.request.kind === 'error' ? (props.request.message || t('admin.accounts.recentRequests.unknownError')) : '—',
    mono: false
  }
])
</script>
