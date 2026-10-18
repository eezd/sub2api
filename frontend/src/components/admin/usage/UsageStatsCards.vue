<template>
  <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
    <div class="card flex flex-col p-5">
      <div class="flex items-start justify-between gap-2">
        <p class="stat-label">{{ t('usage.totalRequests') }}</p>
        <span class="rounded-lg bg-primary-50 p-1.5 text-primary-600 ring-1 ring-inset ring-primary-500/15 dark:bg-blue-900/30"><Icon name="document" size="sm" /></span>
      </div>
      <p class="mt-3 text-2xl font-semibold tracking-tight tabular-nums text-slate-900 dark:text-white sm:text-[28px]">{{ stats?.total_requests?.toLocaleString() || '0' }}</p>
      <p class="mt-auto border-t border-slate-200/50 pt-3 text-xs text-slate-500 dark:border-dark-700 dark:text-gray-400">{{ t('usage.inSelectedRange') }}</p>
    </div>
    <div class="card flex flex-col p-5">
      <div class="flex items-start justify-between gap-2">
        <p class="stat-label">{{ t('usage.totalTokens') }}</p>
        <span class="rounded-lg bg-amber-50 p-1.5 text-amber-600 ring-1 ring-inset ring-amber-500/15 dark:bg-amber-900/30"><svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9" /></svg></span>
      </div>
      <div class="flex flex-1 flex-col">
        <p class="mt-3 text-2xl font-semibold tracking-tight tabular-nums text-slate-900 dark:text-white sm:text-[28px]">{{ formatTokens(stats?.total_tokens || 0) }}</p>
        <p class="mt-auto flex flex-wrap items-center gap-x-1 border-t border-slate-200/50 pt-3 font-mono text-xs tabular-nums text-slate-500 dark:border-dark-700">
          <span>{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
          <span>/</span>
          <span>{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
          <span>/</span>
          <span class="group relative inline-flex cursor-help items-center gap-0.5" tabindex="0">
            <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
            <svg
              class="h-3.5 w-3.5 text-gray-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            <span
              class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 hidden w-56 -translate-x-1/2 rounded-xl border border-white bg-white/95 p-3 text-left font-sans text-xs text-slate-700 shadow-glass-lg backdrop-blur-2xl group-hover:block group-focus:block dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
            >
              <span class="mb-2 block font-medium text-gray-900 dark:text-white">
                {{ cacheDetailLabel() }}
              </span>
              <span class="flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheCreationTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_creation_tokens || 0) }}
                </span>
              </span>
              <span class="mt-1 flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheReadTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_read_tokens || 0) }}
                </span>
              </span>
            </span>
          </span>
        </p>
      </div>
    </div>
    <div class="card flex flex-col p-5">
      <div class="flex items-start justify-between gap-2">
        <p class="stat-label">{{ t('usage.totalCost') }}</p>
        <span class="rounded-lg bg-emerald-50 p-1.5 text-emerald-600 ring-1 ring-inset ring-emerald-500/15 dark:bg-green-900/30"><Icon name="dollar" size="sm" /></span>
      </div>
      <div class="flex min-w-0 flex-1 flex-col">
        <p class="mt-3 truncate text-2xl font-semibold tracking-tight tabular-nums text-slate-900 dark:text-white sm:text-[28px]">
          ${{ (stats?.total_actual_cost || 0).toFixed(4) }}
        </p>
        <p class="mt-auto border-t border-slate-200/50 pt-3 font-mono text-xs tabular-nums text-slate-400 dark:border-dark-700">
          <template v-if="showAccountCost && totalAccountCost != null">
            <span class="text-orange-500">{{ t('usage.accountCost') }} ${{ totalAccountCost.toFixed(4) }}</span>
            <span> · </span>
          </template>
          <span>
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
        </p>
      </div>
    </div>
    <div class="card flex flex-col p-5">
      <div class="flex items-start justify-between gap-2">
        <p class="stat-label">{{ t('usage.avgDuration') }}</p>
        <span class="rounded-lg bg-violet-50 p-1.5 text-violet-600 ring-1 ring-inset ring-violet-500/15 dark:bg-purple-900/30"><Icon name="clock" size="sm" /></span>
      </div>
      <p class="mt-3 text-2xl font-semibold tracking-tight tabular-nums text-slate-900 dark:text-white sm:text-[28px]">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import type { UsageStatsResponse } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
})

const { t } = useI18n()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)

const formatDuration = (ms: number) =>
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}

const cacheLabel = () => t('usage.cacheTotal')
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>
