<template>
      <div
        v-if="previewDocument"
        data-test="svg-animation-result"
        class="overflow-hidden rounded-xl border border-violet-200 bg-white shadow-sm dark:border-violet-900"
      >
        <div class="flex items-center justify-between border-b border-violet-100 px-3 py-2 dark:border-violet-900 dark:bg-dark-800">
          <div class="flex items-center gap-2 text-xs font-semibold text-violet-900 dark:text-violet-100">
            <span class="h-2 w-2 rounded-full bg-emerald-500"></span>
            {{ createdAt ? t('admin.accounts.svgAnimationTest.historyPreview', { time: formatHistoryTime(createdAt) }) : t('admin.accounts.svgAnimationTest.preview') }}
          </div>
          <span class="text-[10px] text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.svgAnimationTest.sandboxed') }}
          </span>
        </div>
        <iframe
          data-test="svg-animation-frame"
          :title="t('admin.accounts.svgAnimationTest.previewTitle')"
          :srcdoc="previewDocument"
          sandbox=""
          referrerpolicy="no-referrer"
          class="aspect-[16/10] min-h-[320px] w-full bg-white"
        ></iframe>
      </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { buildPreviewDocument } from '@/utils/degradationChecks'

const props = defineProps<{ outputText: string; createdAt?: string }>()
const { t } = useI18n()
const previewDocument = computed(() => buildPreviewDocument(props.outputText))
const formatHistoryTime = (value: string) => new Date(value).toLocaleString()
</script>
