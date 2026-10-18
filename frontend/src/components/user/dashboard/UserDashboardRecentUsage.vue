<template>
  <div class="card overflow-hidden">
    <div class="flex items-center justify-between gap-3 px-6 pb-3 pt-5">
      <div class="flex items-center gap-2">
        <h2 class="text-base font-semibold tracking-tight text-slate-900 dark:text-white">{{ t('dashboard.recentUsage') }}</h2>
        <span class="badge badge-gray">{{ t('dashboard.last7Days') }}</span>
      </div>
      <router-link v-if="data.length > 0" to="/usage" class="flex items-center gap-1 text-xs font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300">
        {{ t('dashboard.viewAllUsage') }}
        <Icon name="arrowRight" size="xs" />
      </router-link>
    </div>
    <div v-if="loading" class="flex items-center justify-center py-12">
      <LoadingSpinner size="lg" />
    </div>
    <div v-else-if="data.length === 0" class="px-6 pb-8 pt-4">
      <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
    </div>
    <div v-else class="overflow-x-auto px-3 pb-3">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-left text-xs font-semibold tracking-wide text-slate-500 dark:text-dark-400">
            <th class="px-3 py-2.5 font-semibold">{{ t('usage.time') }}</th>
            <th class="px-3 py-2.5 font-semibold">{{ t('usage.model') }}</th>
            <th class="px-3 py-2.5 text-right font-semibold">{{ t('usage.tokens') }}</th>
            <th class="px-3 py-2.5 text-right font-semibold">{{ t('usage.cost') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="log in data" :key="log.id" class="border-t border-slate-200/40 transition-colors hover:bg-white/70 dark:border-dark-700 dark:hover:bg-dark-800/40">
            <td class="whitespace-nowrap px-3 py-3 font-mono text-xs tabular-nums text-slate-500 dark:text-dark-400">{{ formatDateTime(log.created_at) }}</td>
            <td class="px-3 py-3">
              <span class="inline-flex max-w-[16rem] items-center gap-1.5 truncate rounded-full bg-primary-50 px-2.5 py-0.5 font-mono text-xs font-medium text-primary-700 ring-1 ring-inset ring-primary-500/20 dark:bg-primary-900/30 dark:text-primary-300 dark:ring-0">
                <span class="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-primary-500"></span>
                <span class="truncate">{{ log.model }}</span>
              </span>
            </td>
            <td class="whitespace-nowrap px-3 py-3 text-right font-mono text-xs tabular-nums text-slate-600 dark:text-gray-300">{{ (log.input_tokens + log.output_tokens).toLocaleString() }}</td>
            <td class="whitespace-nowrap px-3 py-3 text-right font-mono text-xs tabular-nums">
              <span class="font-semibold text-slate-900 dark:text-white" :title="t('dashboard.actual')">${{ formatCost(log.actual_cost) }}</span>
              <span class="ml-1 text-slate-400 line-through decoration-slate-300 dark:text-gray-500" :title="t('dashboard.standard')">${{ formatCost(log.total_cost) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { UsageLog } from '@/types'

defineProps<{
  data: UsageLog[]
  loading: boolean
}>()
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)
</script>
