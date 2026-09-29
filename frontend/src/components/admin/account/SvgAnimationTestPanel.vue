<template>
  <section
    data-test="svg-animation-test-panel"
    class="overflow-hidden rounded-xl border border-violet-200 bg-violet-50/60 dark:border-violet-900/70 dark:bg-violet-950/20"
  >
    <div class="border-b border-violet-200/80 px-4 py-3 dark:border-violet-900/70">
      <div class="flex items-start gap-3">
        <div
          class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-violet-600 text-white shadow-sm shadow-violet-900/20"
        >
          <Icon name="eye" size="sm" :stroke-width="2" />
        </div>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="text-sm font-semibold text-violet-950 dark:text-violet-100">
              {{ t('admin.accounts.svgAnimationTest.title') }}
            </h3>
            <span
              class="rounded border border-violet-300 bg-white/80 px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider text-violet-700 dark:border-violet-800 dark:bg-violet-950/60 dark:text-violet-300"
            >
              SVG
            </span>
          </div>
          <p class="mt-1 text-xs leading-5 text-violet-800/80 dark:text-violet-200/70">
            {{ t('admin.accounts.svgAnimationTest.description') }}
          </p>
        </div>
      </div>
    </div>

    <div class="space-y-3 p-4">
      <div
        class="rounded-lg border border-violet-200 bg-white/80 px-3 py-2.5 dark:border-violet-900 dark:bg-dark-800/70"
      >
        <div class="mb-1 text-[10px] font-semibold uppercase tracking-wider text-violet-600 dark:text-violet-300">
          {{ t('admin.accounts.svgAnimationTest.promptLabel') }}
        </div>
        <p data-test="svg-animation-prompt" class="font-mono text-xs leading-5 text-gray-700 dark:text-gray-200">
          {{ SVG_ANIMATION_PROMPT }}
        </p>
      </div>

      <div class="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
        <div class="space-y-1.5">
          <label class="text-xs font-semibold uppercase tracking-wide text-violet-900 dark:text-violet-200">
            {{ t('admin.accounts.svgAnimationTest.selectModel') }}
          </label>
          <Select
            v-model="selectedModelId"
            data-test="svg-animation-model"
            :options="modelSelectOptions"
            :disabled="disabled || status === 'running'"
            value-key="id"
            label-key="display_name"
            :placeholder="t('admin.accounts.svgAnimationTest.selectModelPlaceholder')"
          />
        </div>
        <button
          type="button"
          data-test="svg-animation-start"
          class="inline-flex h-10 items-center justify-center gap-2 rounded-lg bg-violet-700 px-4 text-sm font-semibold text-white transition-colors hover:bg-violet-800 disabled:cursor-not-allowed disabled:bg-violet-300 dark:bg-violet-600 dark:hover:bg-violet-500 dark:disabled:bg-violet-900"
          :disabled="!canStart"
          @click="start"
        >
          <Icon
            :name="status === 'running' ? 'refresh' : 'play'"
            size="sm"
            :class="status === 'running' ? 'animate-spin' : ''"
            :stroke-width="2"
          />
          {{ status === 'running' ? t('admin.accounts.svgAnimationTest.running') : t('admin.accounts.svgAnimationTest.start') }}
        </button>
      </div>

      <p class="text-[11px] leading-4 text-violet-800/70 dark:text-violet-300/60">
        {{ t('admin.accounts.svgAnimationTest.costHint') }}
      </p>

      <div
        v-if="status === 'running'"
        data-test="svg-animation-progress"
        class="flex items-center gap-2 rounded-lg border border-violet-200 bg-white/80 p-3 text-xs text-violet-800 dark:border-violet-900 dark:bg-dark-800/70 dark:text-violet-200"
        aria-live="polite"
      >
        <Icon name="refresh" size="sm" class="shrink-0 animate-spin" :stroke-width="2" />
        <span>{{ t('admin.accounts.svgAnimationTest.receiving', { count: responseText.length }) }}</span>
      </div>

      <div
        v-if="status === 'error'"
        data-test="svg-animation-error"
        class="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-300"
        role="alert"
      >
        <Icon name="x" size="sm" class="mt-0.5 shrink-0" :stroke-width="2" />
        <span class="break-words">{{ errorMessage }}</span>
      </div>

      <div
        v-if="previewDocument"
        data-test="svg-animation-result"
        class="overflow-hidden rounded-xl border border-violet-200 bg-white shadow-sm dark:border-violet-900"
      >
        <div class="flex items-center justify-between border-b border-violet-100 px-3 py-2 dark:border-violet-900 dark:bg-dark-800">
          <div class="flex items-center gap-2 text-xs font-semibold text-violet-900 dark:text-violet-100">
            <span class="h-2 w-2 rounded-full bg-emerald-500"></span>
            {{ previewCreatedAt ? t('admin.accounts.svgAnimationTest.historyPreview', { time: formatHistoryTime(previewCreatedAt) }) : t('admin.accounts.svgAnimationTest.preview') }}
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

      <div
        data-test="svg-animation-history"
        class="rounded-xl border border-violet-200 bg-white/70 p-3 dark:border-violet-900 dark:bg-dark-800/60"
      >
        <div class="mb-2 flex items-center justify-between gap-3">
          <div>
            <h4 class="text-xs font-semibold uppercase tracking-wide text-violet-900 dark:text-violet-100">
              {{ t('admin.accounts.svgAnimationTest.historyTitle') }}
            </h4>
            <p class="mt-0.5 text-[10px] text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.svgAnimationTest.historyHint') }}
            </p>
          </div>
          <Icon v-if="historyLoading" name="refresh" size="sm" class="animate-spin text-violet-500" :stroke-width="2" />
        </div>
        <p v-if="historyError" class="text-xs text-red-600 dark:text-red-300">{{ historyError }}</p>
        <p v-else-if="!historyLoading && history.length === 0" class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.svgAnimationTest.historyEmpty') }}
        </p>
        <div v-else class="space-y-1.5">
          <button
            v-for="item in history"
            :key="item.id"
            type="button"
            class="flex w-full items-center justify-between gap-3 rounded-lg border border-violet-100 px-3 py-2 text-left transition-colors hover:bg-violet-50 disabled:cursor-default disabled:hover:bg-transparent dark:border-violet-950 dark:hover:bg-violet-950/40 dark:disabled:hover:bg-transparent"
            :disabled="item.status !== 'success' || status === 'running'"
            @click="showHistoryResult(item)"
          >
            <span class="min-w-0 flex-1">
              <span class="block truncate text-xs font-medium text-gray-800 dark:text-gray-200">{{ item.requested_model }}</span>
              <span class="block text-[10px] text-gray-500 dark:text-gray-400">{{ formatHistoryTime(item.created_at) }}</span>
            </span>
            <span
              v-if="item.status === 'success'"
              class="shrink-0 rounded-full bg-emerald-100 px-2 py-0.5 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
            >
              {{ t('admin.accounts.svgAnimationTest.historyView') }}
            </span>
            <span
              v-else
              :title="item.error_message || t('admin.accounts.svgAnimationTest.failed')"
              class="min-w-0 max-w-[60%] truncate rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-700 dark:bg-red-950 dark:text-red-300"
            >
              {{ item.error_message || t('admin.accounts.svgAnimationTest.failed') }}
            </span>
          </button>
        </div>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'
import { adminAPI } from '@/api/admin'
import type { AccountDegradationCheckResult } from '@/api/admin/accounts'
import Select from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import type { ClaudeModel } from '@/types'

const SVG_ANIMATION_PROMPT = "Create an HTML with content that's an SVG drawing of a 2D animation of a pelican riding a bicycle."
const PREVIEW_CSP = "default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline';"

const props = defineProps<{
  active: boolean
  accountId: number
  models: ClaudeModel[]
  disabled?: boolean
}>()

const emit = defineEmits<{
  (event: 'running-change', running: boolean): void
}>()

const { t } = useI18n()
const selectedModelId = ref('')
const status = ref<'idle' | 'running' | 'success' | 'error'>('idle')
const responseText = ref('')
const previewDocument = ref('')
const errorMessage = ref('')
const previewCreatedAt = ref('')
const history = ref<AccountDegradationCheckResult[]>([])
const historyLoading = ref(false)
const historyError = ref('')
let abortController: AbortController | null = null

const modelSelectOptions = computed(() => props.models.map((model) => ({
  id: model.id,
  display_name: model.display_name
})))

const canStart = computed(() => {
  if (props.disabled || status.value === 'running' || !selectedModelId.value) return false
  return props.models.some((model) => model.id === selectedModelId.value)
})

const formatHistoryTime = (value: string) => new Date(value).toLocaleString()

const loadHistory = async () => {
  if (!props.active || props.accountId <= 0) return
  const accountId = props.accountId
  historyLoading.value = true
  historyError.value = ''
  try {
    const items = await adminAPI.accounts.getDegradationCheckHistory(accountId, 'svg_animation', 10)
    if (props.active && props.accountId === accountId) history.value = items
  } catch (error) {
    if (props.active && props.accountId === accountId) {
      historyError.value = error instanceof Error ? error.message : t('admin.accounts.svgAnimationTest.historyFailed')
    }
  } finally {
    if (props.accountId === accountId) historyLoading.value = false
  }
}

const showHistoryResult = (item: AccountDegradationCheckResult) => {
  if (item.status !== 'success' || !item.output_text || status.value === 'running') return
  const document = buildPreviewDocument(item.output_text)
  if (!document) {
    fail(t('admin.accounts.svgAnimationTest.noSvg'))
    return
  }
  previewDocument.value = document
  previewCreatedAt.value = item.created_at
  status.value = 'success'
}

const stripUnsafeMarkup = (markup: string) => markup
  .replace(/<script\b[^>]*>[\s\S]*?<\/script\s*>/gi, '')
  .replace(/<script\b[^>]*\/\s*>/gi, '')
  .replace(/<base\b[^>]*>/gi, '')
  .replace(/<meta\b(?=[^>]*http-equiv\s*=\s*["']?refresh\b)[^>]*>/gi, '')
  .replace(/<meta\b(?=[^>]*http-equiv\s*=\s*["']?content-security-policy\b)[^>]*>/gi, '')

// The policy must be the first element the parser sees: it then lands in the
// implicit <head> before any model-controlled markup can request a resource.
// Model-supplied doctype/html/head tags after it become ignorable parse errors.
const addPreviewPolicy = (markup: string) =>
  `<!doctype html><meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">${markup}`

const buildPreviewDocument = (raw: string) => {
  const fencedBlocks = [...raw.matchAll(/```(?:html|svg|xml)?\s*([\s\S]*?)```/gi)].map((match) => match[1])
  const candidates = [...fencedBlocks, raw]

  for (const candidate of candidates) {
    const starts = [candidate.search(/<!doctype\s+html/i), candidate.search(/<html(?:\s|>)/i), candidate.search(/<svg(?:\s|>)/i)]
      .filter((index) => index >= 0)
    if (starts.length === 0) continue

    const markup = candidate.slice(Math.min(...starts)).trim()
    if (!/<svg(?:\s|>)/i.test(markup)) continue
    return addPreviewPolicy(stripUnsafeMarkup(markup))
  }

  return ''
}

const reset = (clearModel = false) => {
  if (abortController) {
    abortController.abort()
    abortController = null
    emit('running-change', false)
  }
  if (clearModel) selectedModelId.value = ''
  status.value = 'idle'
  responseText.value = ''
  previewDocument.value = ''
  errorMessage.value = ''
  previewCreatedAt.value = ''
}

const fail = (message: string) => {
  status.value = 'error'
  errorMessage.value = message
}

const handleEvent = (event: {
  type: string
  success?: boolean
  error?: string
  data?: AccountDegradationCheckResult
}) => {
  switch (event.type) {
    case 'svg_animation_complete': {
      if (!event.success || !event.data) {
        fail(event.error || t('admin.accounts.svgAnimationTest.failed'))
        return
      }
      responseText.value = event.data.output_text || ''
      const document = buildPreviewDocument(responseText.value)
      if (!document) {
        fail(t('admin.accounts.svgAnimationTest.noSvg'))
        return
      }
      previewDocument.value = document
      previewCreatedAt.value = event.data.created_at
      history.value = [event.data, ...history.value.filter((item) => item.id !== event.data?.id)].slice(0, 10)
      status.value = 'success'
      break
    }
    case 'error':
      fail(event.error || t('admin.accounts.svgAnimationTest.failed'))
      break
  }
}

const start = async () => {
  if (!canStart.value) return

  reset()
  status.value = 'running'
  emit('running-change', true)
  const controller = new AbortController()
  abortController = controller

  try {
    const response = await fetch(buildApiUrl(`/admin/accounts/${props.accountId}/svg-animation-test`), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
        'Content-Type': 'application/json',
        [ADMIN_UI_REQUEST_HEADER]: '1'
      },
      body: JSON.stringify({ model_id: selectedModelId.value }),
      signal: controller.signal
    })

    if (!response.ok) throw new Error(`HTTP error! status: ${response.status}`)
    const reader = response.body?.getReader()
    if (!reader) throw new Error(t('admin.accounts.svgAnimationTest.noResponseBody'))

    const decoder = new TextDecoder()
    let buffer = ''
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (!line.startsWith('data:')) continue
        const payload = line.slice(5).trim()
        if (!payload || payload === '[DONE]') continue
        try {
          handleEvent(JSON.parse(payload))
        } catch (error) {
          console.error('Failed to parse SVG animation test event:', error)
        }
      }
    }

    const trailingPayload = buffer.startsWith('data:') ? buffer.slice(5).trim() : ''
    if (trailingPayload && trailingPayload !== '[DONE]') {
      try {
        handleEvent(JSON.parse(trailingPayload))
      } catch (error) {
        console.error('Failed to parse trailing SVG animation test event:', error)
      }
    }
    if (status.value === 'running') fail(t('admin.accounts.svgAnimationTest.streamEnded'))
  } catch (error: unknown) {
    if (abortController !== controller) return
    if (error instanceof DOMException && error.name === 'AbortError') {
      status.value = 'idle'
      return
    }
    fail(error instanceof Error ? error.message : t('admin.accounts.svgAnimationTest.failed'))
  } finally {
    if (abortController === controller) {
      abortController = null
      emit('running-change', false)
    }
    if (props.active) void loadHistory()
  }
}

watch(
  () => [props.active, props.accountId] as const,
  ([active]) => {
    history.value = []
    historyError.value = ''
    if (active) {
      reset(true)
      void loadHistory()
    } else {
      reset()
    }
  },
  { immediate: true }
)

onBeforeUnmount(() => reset())
</script>
