<template>
  <BaseDialog :show="show" :title="t('admin.accounts.degradationBatch.title')" width="extra-wide" @close="close">
    <div class="space-y-4" data-testid="bulk-degradation-modal">
      <p class="rounded-lg bg-primary-50 p-3 text-sm dark:bg-primary-900/20">{{ t('admin.accounts.degradationBatch.background') }}</p>
      <div class="flex flex-wrap gap-2">
        <button v-if="accountIds.length" class="btn btn-secondary btn-sm" :disabled="submitting" @click="reopenPreparation">{{ t(accepted ? 'admin.accounts.degradationBatch.newBatch' : 'admin.accounts.degradationBatch.prepare') }}</button>
        <button class="btn btn-secondary btn-sm" data-testid="batch-tasks" @click="showTasks">{{ t('admin.accounts.degradationBatch.tasks') }}</button>
      </div>
      <template v-if="view === 'prepare'">
        <h4 class="font-semibold">{{ t(`admin.accounts.degradationBatch.${checkType}`) }}</h4>
        <p v-if="rows.length > 1000" role="alert" class="text-red-600">{{ t('admin.accounts.degradationBatch.limit') }}</p>
        <fieldset :disabled="submitting" class="space-y-4">
          <div v-for="platform in platforms" :key="platform" class="flex flex-wrap items-center gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700">
            <strong>{{ platform }}</strong>
            <Select class="min-w-48 flex-1" v-model="fills[platform]" :options="platformOptions(platform)" :placeholder="t('admin.accounts.degradationBatch.selectModel')" />
            <button class="btn btn-secondary btn-sm" :disabled="!fills[platform]" @click="applyPlatform(platform)">{{ t('admin.accounts.degradationBatch.applyPlatform') }}</button>
            <span v-if="unapplied[platform] !== undefined" class="text-sm">{{ t('admin.accounts.degradationBatch.unapplied', { count: unapplied[platform] }) }}</span>
          </div>
          <div v-for="row in rows" :key="row.id" class="grid gap-2 rounded-lg border border-gray-200 p-3 dark:border-dark-700 sm:grid-cols-2" :data-account-id="row.id">
            <div><strong>{{ row.name || `#${row.id}` }}</strong><div class="text-sm text-gray-500">#{{ row.id }} · {{ row.platform }} · {{ row.type }}</div></div>
            <Select v-if="row.state === 'ready'" :model-value="row.model" :options="row.models.map(model => ({ value: model.id, label: model.display_name || model.id }))" :placeholder="t('admin.accounts.degradationBatch.selectModel')" clearable :aria-label="`${t('admin.accounts.degradationBatch.selectModel')} #${row.id}`" @update:model-value="row.model = typeof $event === 'string' ? $event : null" />
            <div v-else class="text-sm"><span>{{ t(`admin.accounts.degradationBatch.${row.reason || 'loading'}`) }}</span><span v-if="row.error" class="block text-red-600">{{ row.error }}</span><button v-if="row.state === 'error'" class="btn btn-secondary btn-sm" :disabled="preparing > 0" @click="retryRow(row)">{{ t('admin.accounts.degradationBatch.reload') }}</button></div>
          </div>
        </fieldset>
        <p>{{ t('admin.accounts.degradationBatch.executionSummary', { run: runnable, skip: rows.length - runnable }) }}</p>
        <p v-if="submissionError" role="alert" class="text-red-600">{{ submissionError }} {{ t('admin.accounts.degradationBatch.unknownSubmission') }}</p>
        <button class="btn btn-primary" data-testid="batch-start" :disabled="!canStart || submitting" @click="submit">{{ t(submitting ? 'admin.accounts.degradationBatch.submitting' : 'admin.accounts.degradationBatch.start') }}</button>
      </template>
      <template v-else>
        <div class="flex flex-wrap items-center gap-2">
          <button v-if="query.batchId.value !== null" class="btn btn-secondary btn-sm" @click="openBatch(null)">{{ t('admin.accounts.degradationBatch.back') }}</button>
          <button class="btn btn-secondary btn-sm" :disabled="query.loading.value" @click="query.refresh">{{ t('admin.accounts.degradationBatch.refresh') }}</button>
          <span v-if="query.loading.value">{{ t('admin.accounts.degradationBatch.loading') }}</span>
        </div>
        <p v-if="query.stale.value" role="alert" class="text-red-600">{{ t('admin.accounts.degradationBatch.stale') }} {{ query.error.value }}</p>
        <template v-if="query.batchId.value === null">
          <button v-for="task in query.batches.value" :key="task.id" class="block w-full rounded-lg border border-gray-200 p-3 text-left dark:border-dark-700" :data-batch-id="task.id" @click="openBatch(task.id)">
            <strong>#{{ task.id }} · {{ t(`admin.accounts.degradationBatch.${task.check_type}`) }}</strong><div>{{ task.created_at }} · {{ t(`admin.accounts.degradationBatch.status.${task.status}`) }}</div><p class="text-sm">{{ countsText(task) }}</p>
          </button>
          <p v-if="!query.loading.value && !query.batches.value.length">{{ t('admin.accounts.degradationBatch.empty') }}</p>
          <Pagination v-if="query.total.value > 0" :total="query.total.value" :page="query.page.value" :page-size="10" :show-page-size-selector="false" @update:page="query.changePage" />
        </template>
        <template v-else-if="query.batch.value">
          <div class="flex flex-wrap items-center justify-between gap-2"><h4 class="font-semibold">#{{ query.batch.value.id }} · {{ t(`admin.accounts.degradationBatch.status.${query.batch.value.status}`) }}</h4><button v-if="!query.terminal(query.batch.value)" class="btn btn-danger btn-sm" :disabled="canceling" data-testid="batch-cancel" @click="openCancelConfirmation">{{ t('admin.accounts.degradationBatch.cancel') }}</button></div>
          <p>{{ countsText(query.batch.value) }}</p>
          <div v-for="item in query.items.value" :key="item.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700" :data-item-id="item.id">
            <div class="flex flex-wrap items-center justify-between gap-2"><strong>{{ item.account_name }} #{{ item.account_id }}</strong><span>{{ t(`admin.accounts.degradationBatch.status.${item.status}`) }}</span></div>
            <p class="text-sm">{{ item.platform }} · {{ item.requested_model }}<span v-if="item.tested_model"> → {{ item.tested_model }}</span></p>
            <p v-if="item.status === 'running' && query.batch.value.check_type === 'model_trace'">{{ t('admin.accounts.degradationBatch.progress', { attempt: item.progress.attempt || 0, max: item.progress.max_attempts || 6, received: item.progress.received || 0, target: item.progress.target || 3 }) }}</p>
            <p v-else-if="item.status === 'running'">{{ t('admin.accounts.degradationBatch.generating') }}</p>
            <p v-if="item.model_trace_summary">{{ item.model_trace_summary.prediction_name }} · {{ (item.model_trace_summary.probability * 100).toFixed(1) }}% · {{ t(`admin.accounts.degradationBatch.${item.model_trace_summary.matches_expected === null ? 'unknown' : item.model_trace_summary.matches_expected ? 'match' : 'mismatch'}`) }}</p>
            <p v-if="item.reason_code">{{ t(`admin.accounts.degradationBatch.reasons.${item.reason_code}`) }}</p><p v-if="item.error_message" class="text-red-600">{{ item.error_message }}</p>
            <button v-if="!['pending', 'running'].includes(item.status)" class="btn btn-secondary btn-sm" @click="query.openItem(item.id)">{{ t('admin.accounts.degradationBatch.result') }}</button>
          </div>
          <Pagination v-if="query.itemTotal.value > 0" :total="query.itemTotal.value" :page="query.itemPage.value" :page-size="50" :show-page-size-selector="false" @update:page="query.changePage" />
          <section v-if="query.detail.value" class="space-y-3 rounded-lg bg-gray-50 p-4 dark:bg-dark-800" data-testid="batch-result">
            <div class="flex justify-between"><h4>{{ query.detail.value.account_name }} #{{ query.detail.value.account_id }}</h4><button class="btn btn-secondary btn-sm" @click="query.openItem(null)">{{ t('common.close') }}</button></div>
            <ModelTraceResultPanel v-if="traceResult" :result="traceResult" />
            <SvgAnimationResultPreview v-if="query.detail.value.status === 'succeeded' && query.batch.value.check_type === 'svg_animation'" :output-text="query.detail.value.output_text" :created-at="query.detail.value.finished_at || undefined" />
            <pre v-else-if="query.detail.value.output_text" class="max-h-80 overflow-auto whitespace-pre-wrap break-words text-sm">{{ query.detail.value.output_text }}</pre>
            <p v-if="query.detail.value.error_message" class="text-red-600">{{ query.detail.value.error_message }}</p>
          </section>
        </template>
        <p v-if="cancelError && cancelErrorBatchId === query.batchId.value" role="alert" class="text-red-600">{{ cancelError }}</p>
      </template>
    </div>
    <template #footer><button class="btn btn-secondary" @click="close">{{ t('common.close') }}</button></template>
  </BaseDialog>
  <ConfirmDialog :show="show && confirmCancel" :title="t('admin.accounts.degradationBatch.cancel')" :message="t('admin.accounts.degradationBatch.cancelConfirm')" danger @confirm="cancelBatch" @cancel="dismissCancelConfirmation" />
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AccountDegradationCheckType, DegradationBatch, DegradationBatchCreateRequest } from '@/api/admin/accounts'
import type { ClaudeModel } from '@/types'
import { useAppStore } from '@/stores/app'
import { useDegradationCheckBatches } from '@/composables/useDegradationCheckBatches'
import { supportsDegradationCheckAccount, filterDegradationCheckModels, type ModelTraceResult } from '@/utils/degradationChecks'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ModelTraceResultPanel from './ModelTraceResultPanel.vue'
import SvgAnimationResultPreview from './SvgAnimationResultPreview.vue'

interface PreparationRow {
  id: number
  name: string
  platform: string
  type: string
  state: 'loading' | 'ready' | 'skipped' | 'error'
  reason: string
  error: string
  models: ClaudeModel[]
  model: string | null
}
const props = defineProps<{ show: boolean; accountIds: number[]; checkType: AccountDegradationCheckType }>()
const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()
const appStore = useAppStore()
const view = ref<'prepare' | 'tasks'>('tasks')
const rows = ref<PreparationRow[]>([])
const preparing = ref(0)
const fills = ref<Record<string, string | number | boolean | null>>({})
const unapplied = ref<Record<string, number>>({})
const submitting = ref(false)
const accepted = ref(false)
const submissionError = ref('')
const cancelError = ref('')
const cancelErrorBatchId = ref<number | null>(null)
const canceling = ref(false)
const confirmCancel = ref(false)
const pendingCancelBatchId = ref<number | null>(null)
let generation = 0
let submittedPayload = ''
let requestKey = ''
function errorMessage(cause: unknown) {
  if (cause && typeof cause === 'object' && 'message' in cause && typeof cause.message === 'string') return cause.message
  return String(cause)
}
const queryVisible = computed(() => props.show && view.value === 'tasks')
const query = useDegradationCheckBatches(queryVisible)
function clearCancelError() {
  cancelError.value = ''
  cancelErrorBatchId.value = null
}
function openBatch(id: number | null) {
  clearCancelError()
  query.openBatch(id)
}
const platforms = computed(() => [...new Set(rows.value.filter(row => row.state === 'ready').map(row => row.platform))])
const runnable = computed(() => rows.value.filter(row => row.state === 'ready').length)
const canStart = computed(() => rows.value.length > 0 && rows.value.length <= 1000 && preparing.value === 0 && runnable.value > 0 && rows.value.every(row => row.state === 'skipped' || (row.state === 'ready' && row.model !== null)))
const traceResult = computed(() => query.batch.value?.check_type === 'model_trace' && query.detail.value?.status === 'succeeded' && query.detail.value.result ? query.detail.value.result as unknown as ModelTraceResult : null)
function countsText(task: DegradationBatch) {
  const c = task.counts
  return t('admin.accounts.degradationBatch.counts', { done: c.total - c.pending - c.running, ...c })
}
function platformOptions(platform: string) {
  const models = new Map<string, number>()
  for (const row of rows.value) {
    if (row.state !== 'ready' || row.platform !== platform) continue
    for (const model of row.models) models.set(model.id, (models.get(model.id) || 0) + 1)
  }
  return [...models].map(([value, count]) => ({ value, label: `${value} (${count})` }))
}
function applyPlatform(platform: string) {
  const model = fills.value[platform]
  let missed = 0
  for (const row of rows.value) {
    if (row.platform !== platform || row.state !== 'ready') continue
    if (typeof model === 'string' && row.models.some(option => option.id === model)) row.model = model
    else missed++
  }
  unapplied.value[platform] = missed
}
async function prepareRow(row: PreparationRow, token: number) {
  try {
    const account = await adminAPI.accounts.getById(row.id)
    if (token !== generation) return
    row.name = account.name
    row.platform = account.platform
    row.type = account.type
    if (!supportsDegradationCheckAccount(account)) {
      row.state = 'skipped'
      row.reason = 'unsupported'
      return
    }
    try {
      const models = await adminAPI.accounts.getAvailableModels(row.id)
      if (token !== generation) return
      row.models = filterDegradationCheckModels(models)
      row.state = row.models.length ? 'ready' : 'skipped'
      row.reason = row.models.length ? '' : 'noModels'
    } catch (cause) {
      if (token !== generation) return
      row.state = 'error'
      row.reason = 'catalogError'
      row.error = errorMessage(cause)
    }
  } catch (cause) {
    if (token !== generation) return
    const status = (cause as { response?: { status?: number }; status?: number }).response?.status || (cause as { status?: number }).status
    row.state = status === 404 ? 'skipped' : 'error'
    row.reason = status === 404 ? 'deleted' : 'accountError'
    row.error = status === 404 ? '' : errorMessage(cause)
  }
}
async function prepare() {
  accepted.value = false
  const token = ++generation
  rows.value = [...props.accountIds].map(id => ({ id, name: '', platform: '', type: '', state: 'loading', reason: '', error: '', models: [], model: null }))
  fills.value = {}
  unapplied.value = {}
  submissionError.value = ''
  preparing.value = rows.value.length
  let next = 0
  const snapshot = rows.value
  await Promise.all(Array.from({ length: Math.min(4, snapshot.length) }, async () => {
    while (next < snapshot.length && token === generation) {
      const row = snapshot[next++]
      await prepareRow(row, token)
      if (token === generation) preparing.value--
    }
  }))
}
async function retryRow(row: PreparationRow) {
  if (preparing.value || submitting.value) return
  const token = generation
  row.state = 'loading'
  row.reason = ''
  row.error = ''
  row.model = null
  preparing.value++
  await prepareRow(row, token)
  if (token === generation) preparing.value--
}
async function submit() {
  if (!canStart.value || submitting.value) return
  const token = generation
  const payload: DegradationBatchCreateRequest = { check_type: props.checkType, items: rows.value.map(row => ({ account_id: row.id, model_id: row.state === 'ready' ? row.model : null })) }
  const serialized = JSON.stringify(payload)
  if (serialized !== submittedPayload) {
    const nextKey = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    requestKey = nextKey
    submittedPayload = serialized
  }
  const key = requestKey
  submitting.value = true
  submissionError.value = ''
  try {
    const task = await adminAPI.accounts.createDegradationCheckBatch(payload, key)
    appStore.showSuccess(t('admin.accounts.degradationBatch.accepted', { id: task.id }))
    if (!props.show || token !== generation) return
    accepted.value = true
    view.value = 'tasks'
    openBatch(task.id)
  } catch (cause) {
    if (token === generation && props.show) submissionError.value = errorMessage(cause)
  } finally {
    submitting.value = false
  }
}
function dismissCancelConfirmation() {
  confirmCancel.value = false
  pendingCancelBatchId.value = null
}
function reopenPreparation() {
  dismissCancelConfirmation()
  clearCancelError()
  view.value = 'prepare'
  if (accepted.value) void prepare()
}
function showTasks() {
  dismissCancelConfirmation()
  view.value = 'tasks'
  openBatch(null)
}
function openCancelConfirmation() {
  const id = query.batchId.value
  if (id === null) return
  clearCancelError()
  pendingCancelBatchId.value = id
  confirmCancel.value = true
}
function close() {
  generation++
  dismissCancelConfirmation()
  emit('close')
}
async function cancelBatch() {
  const id = pendingCancelBatchId.value
  dismissCancelConfirmation()
  if (id === null || canceling.value) return
  const token = generation
  canceling.value = true
  clearCancelError()
  try {
    await adminAPI.accounts.cancelDegradationCheckBatch(id)
    if (token === generation && props.show && query.batchId.value === id) await query.refresh()
  } catch (cause) {
    if (token === generation && props.show && query.batchId.value === id) {
      cancelError.value = errorMessage(cause)
      cancelErrorBatchId.value = id
    }
  } finally {
    canceling.value = false
  }
}
watch(() => props.show, show => {
  dismissCancelConfirmation()
  clearCancelError()
  if (!show) {
    generation++
    return
  }
  view.value = props.accountIds.length ? 'prepare' : 'tasks'
  if (props.accountIds.length) void prepare()
  else openBatch(null)
}, { immediate: true })
onUnmounted(() => { generation++ })
</script>
