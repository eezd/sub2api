<template>
  <section :aria-label="t('dashboard.quickActions')">
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <button @click="router.push('/keys')" class="card card-hover group flex w-full items-center gap-4 p-4 text-left">
        <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 ring-1 ring-inset ring-primary-500/15 transition-transform group-hover:scale-105 dark:bg-primary-900/30">
          <Icon name="key" size="lg" class="text-primary-600 dark:text-primary-400" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-slate-900 dark:text-white">{{ t('dashboard.createApiKey') }}</p>
          <p class="mt-0.5 truncate text-xs text-slate-500 dark:text-dark-400">{{ t('dashboard.generateNewKey') }}</p>
        </div>
        <Icon
          name="chevronRight"
          size="md"
          class="text-slate-300 transition-colors group-hover:text-primary-500 dark:text-dark-500"
        />
      </button>

      <button @click="router.push('/usage')" class="card card-hover group flex w-full items-center gap-4 p-4 text-left">
        <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-emerald-50 ring-1 ring-inset ring-emerald-500/15 transition-transform group-hover:scale-105 dark:bg-emerald-900/30">
          <Icon name="chart" size="lg" class="text-emerald-600 dark:text-emerald-400" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-slate-900 dark:text-white">{{ t('dashboard.viewUsage') }}</p>
          <p class="mt-0.5 truncate text-xs text-slate-500 dark:text-dark-400">{{ t('dashboard.checkDetailedLogs') }}</p>
        </div>
        <Icon
          name="chevronRight"
          size="md"
          class="text-slate-300 transition-colors group-hover:text-emerald-500 dark:text-dark-500"
        />
      </button>

      <button v-if="canUseBatchImage" @click="router.push('/batch-image')" class="card card-hover group flex w-full items-center gap-4 p-4 text-left">
        <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-sky-50 ring-1 ring-inset ring-sky-500/15 transition-transform group-hover:scale-105 dark:bg-sky-900/30">
          <Icon name="sparkles" size="lg" class="text-sky-600 dark:text-sky-400" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-slate-900 dark:text-white">{{ t('dashboard.batchImageAgent') }}</p>
          <p class="mt-0.5 truncate text-xs text-slate-500 dark:text-dark-400">{{ t('dashboard.batchImageAgentDesc') }}</p>
        </div>
        <Icon
          name="chevronRight"
          size="md"
          class="text-slate-300 transition-colors group-hover:text-sky-500 dark:text-dark-500"
        />
      </button>

      <button @click="router.push('/redeem')" class="card card-hover group flex w-full items-center gap-4 p-4 text-left">
        <div class="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-amber-50 ring-1 ring-inset ring-amber-500/15 transition-transform group-hover:scale-105 dark:bg-amber-900/30">
          <Icon name="gift" size="lg" class="text-amber-600 dark:text-amber-400" />
        </div>
        <div class="min-w-0 flex-1">
          <p class="text-sm font-semibold text-slate-900 dark:text-white">{{ t('dashboard.redeemCode') }}</p>
          <p class="mt-0.5 truncate text-xs text-slate-500 dark:text-dark-400">{{ t('dashboard.addBalanceWithCode') }}</p>
        </div>
        <Icon
          name="chevronRight"
          size="md"
          class="text-slate-300 transition-colors group-hover:text-amber-500 dark:text-dark-500"
        />
      </button>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
const router = useRouter()
const { t } = useI18n()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

onMounted(() => {
  void refreshBatchImageAccess()
})
</script>
