<template>
          <div data-test="model-trace-result" class="space-y-3">
            <div
              :class="[
                'rounded-lg border p-3',
                modelTraceVerdictClass
              ]"
            >
              <div class="flex items-start gap-3">
                <Icon
                  :name="result.matches_expected === false ? 'x' : 'check'"
                  size="md"
                  class="mt-0.5 shrink-0"
                  :stroke-width="2"
                />
                <div>
                  <div class="text-sm font-semibold">{{ modelTraceVerdictLabel }}</div>
                  <p class="mt-1 text-xs opacity-80">
                    {{ t('admin.accounts.modelTrace.resultSummary', {
                      requested: result.requested_model,
                      tested: result.tested_model,
                      prediction: result.prediction_name
                    }) }}
                  </p>
                </div>
              </div>
            </div>

            <dl class="grid grid-cols-2 gap-2 sm:grid-cols-4">
              <div class="rounded-lg border border-cyan-200 bg-white/80 p-2.5 dark:border-cyan-900 dark:bg-dark-800/70">
                <dt class="text-[10px] uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.accounts.modelTrace.prediction') }}</dt>
                <dd class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ result.prediction_name }}</dd>
              </div>
              <div class="rounded-lg border border-cyan-200 bg-white/80 p-2.5 dark:border-cyan-900 dark:bg-dark-800/70">
                <dt class="text-[10px] uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.accounts.modelTrace.probability') }}</dt>
                <dd class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ formatModelTracePercent(result.probability) }}</dd>
              </div>
              <div class="rounded-lg border border-cyan-200 bg-white/80 p-2.5 dark:border-cyan-900 dark:bg-dark-800/70">
                <dt class="text-[10px] uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.accounts.modelTrace.family') }}</dt>
                <dd class="mt-1 truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ result.family_prediction_name }}</dd>
              </div>
              <div class="rounded-lg border border-cyan-200 bg-white/80 p-2.5 dark:border-cyan-900 dark:bg-dark-800/70">
                <dt class="text-[10px] uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.accounts.modelTrace.validQueries') }}</dt>
                <dd class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ result.used_outputs }}/3</dd>
              </div>
            </dl>

            <div class="rounded-lg border border-cyan-200 bg-white/80 p-3 dark:border-cyan-900 dark:bg-dark-800/70">
              <div class="mb-2 text-[10px] font-semibold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.modelTrace.topCandidates') }}
              </div>
              <div class="space-y-2">
                <div v-for="candidate in modelTraceTopCandidates" :key="candidate.model" class="grid grid-cols-[minmax(0,1fr)_4rem] items-center gap-3">
                  <div class="min-w-0">
                    <div class="mb-1 flex items-center justify-between gap-2 text-xs">
                      <span class="truncate font-medium text-gray-800 dark:text-gray-200">{{ candidate.display_name }}</span>
                      <span class="shrink-0 text-[10px] text-gray-500 dark:text-gray-400">{{ candidate.family_name }}</span>
                    </div>
                    <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-600">
                      <div class="h-full rounded-full bg-cyan-600" :style="{ width: `${candidate.probability * 100}%` }"></div>
                    </div>
                  </div>
                  <span class="text-right font-mono text-xs font-semibold text-gray-700 dark:text-gray-300">{{ formatModelTracePercent(candidate.probability) }}</span>
                </div>
              </div>
            </div>

            <p class="border-l-2 border-amber-400 pl-2 text-[11px] leading-4 text-gray-600 dark:text-gray-400">
              {{ t('admin.accounts.modelTrace.disclaimer') }}
            </p>
          </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '@/components/icons'
import { formatModelTracePercent, type ModelTraceResult } from '@/utils/degradationChecks'

const props = defineProps<{ result: ModelTraceResult }>()
const { t } = useI18n()

const modelTraceTopCandidates = computed(() => props.result.results.slice(0, 3) || [])
const modelTraceVerdictLabel = computed(() => {
  if (props.result.matches_expected === true) return t('admin.accounts.modelTrace.match')
  if (props.result.matches_expected === false) return t('admin.accounts.modelTrace.mismatch')
  return t('admin.accounts.modelTrace.unknown')
})
const modelTraceVerdictClass = computed(() => {
  if (props.result.matches_expected === true) {
    return 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-900/70 dark:bg-emerald-950/30 dark:text-emerald-200'
  }
  if (props.result.matches_expected === false) {
    return 'border-red-200 bg-red-50 text-red-800 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-200'
  }
  return 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-200'
})
</script>
