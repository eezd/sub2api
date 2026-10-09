<template>
  <div data-testid="account-scheduling-window-threshold-section">
    <label v-if="showTitle" class="input-label">{{ t('admin.accounts.accountSchedulingWindowThresholdOverride') }}</label>
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label class="input-label text-xs">{{ t('admin.accounts.accountSchedulingThreshold5h') }}</label>
        <input
          v-model="threshold5h"
          data-testid="account-scheduling-threshold-5h"
          type="number"
          min="1"
          max="100"
          class="input"
          :placeholder="t('admin.accounts.accountSchedulingWindowThresholdPlaceholder')"
        />
      </div>
      <div>
        <label class="input-label text-xs">{{ t('admin.accounts.accountSchedulingThreshold7d') }}</label>
        <input
          v-model="threshold7d"
          data-testid="account-scheduling-threshold-7d"
          type="number"
          min="1"
          max="100"
          class="input"
          :placeholder="t('admin.accounts.accountSchedulingWindowThresholdPlaceholder')"
        />
      </div>
    </div>
    <p class="input-hint">{{ t('admin.accounts.accountSchedulingWindowThresholdOverrideHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { SchedulingWindowThresholdInput } from './schedulingWindowThresholds'

// 5h / 周两个窗口阈值输入框，Edit / Create / BulkEdit 共用
withDefaults(defineProps<{ showTitle?: boolean }>(), { showTitle: true })

const threshold5h = defineModel<SchedulingWindowThresholdInput>('threshold5h', { default: '' })
const threshold7d = defineModel<SchedulingWindowThresholdInput>('threshold7d', { default: '' })

const { t } = useI18n()
</script>
