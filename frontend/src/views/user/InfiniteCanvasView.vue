<template>
  <AppLayout>
    <section class="canvas-page -m-4 flex min-h-[calc(100dvh-4rem)] flex-col overflow-hidden bg-gray-50 md:-m-6 lg:-m-8 dark:bg-dark-950">
      <header class="canvas-toolbar">
        <div class="min-w-0">
          <div class="flex items-center gap-2">
            <div class="canvas-mark" aria-hidden="true">
              <Icon name="sparkles" size="md" />
            </div>
            <div class="min-w-0">
              <h1 class="truncate text-base font-semibold text-gray-950 dark:text-white">{{ t('infiniteCanvas.title') }}</h1>
              <p class="truncate text-xs text-gray-500 dark:text-dark-300">{{ t('infiniteCanvas.subtitle') }}</p>
            </div>
          </div>
        </div>

        <div class="flex flex-wrap items-center justify-end gap-2">
          <label class="canvas-key-picker">
            <span class="sr-only">{{ t('infiniteCanvas.chooseKey') }}</span>
            <select
              v-model="selectedKeyId"
              class="canvas-key-select"
              :disabled="loading || !activeKeys.length"
              :aria-label="t('infiniteCanvas.chooseKey')"
              @change="handleKeyChange"
            >
              <option v-for="(key, index) in activeKeys" :key="key.id" :value="key.id">
                {{ index + 1 }} · {{ maskApiKey(key.key) }}
              </option>
            </select>
          </label>
          <span class="canvas-status" :class="canvasReady ? 'canvas-status--ready' : 'canvas-status--connecting'">
            <span class="canvas-status-dot" aria-hidden="true"></span>
            {{ canvasReady ? t('infiniteCanvas.connected') : t('infiniteCanvas.connecting') }}
          </span>
          <button type="button" class="btn btn-secondary btn-sm" data-test="codex-agent-toggle" :aria-expanded="false" :aria-label="t('infiniteCanvas.connectCodex')" @click="openCodex">
            <Icon name="terminal" size="sm" />
            <span class="hidden sm:inline">{{ t('infiniteCanvas.connectCodex') }}</span>
          </button>
          <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('infiniteCanvas.refresh')" @click="refreshCanvas">
            <Icon name="refresh" size="sm" />
            <span class="hidden sm:inline">{{ t('infiniteCanvas.refresh') }}</span>
          </button>
          <button type="button" class="btn btn-secondary btn-sm" :aria-label="t('infiniteCanvas.openNewWindow')" @click="openCanvasInNewWindow">
            <Icon name="externalLink" size="sm" />
            <span class="hidden sm:inline">{{ t('infiniteCanvas.openNewWindow') }}</span>
          </button>
        </div>
      </header>

      <div v-if="loading" class="flex min-h-0 flex-1 items-center justify-center">
        <div class="text-center">
          <LoadingSpinner size="lg" />
          <p class="mt-4 text-sm text-gray-500 dark:text-dark-400">{{ t('infiniteCanvas.loading') }}</p>
        </div>
      </div>

      <div v-else-if="errorMessage" class="flex min-h-0 flex-1 items-center justify-center p-6">
        <div class="max-w-md rounded-2xl border border-gray-200 bg-white p-8 text-center shadow-sm dark:border-dark-700 dark:bg-dark-900">
          <div class="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-amber-100 text-amber-600 dark:bg-amber-900/30 dark:text-amber-300">
            <Icon name="key" size="lg" />
          </div>
          <h2 class="mt-5 text-lg font-semibold text-gray-900 dark:text-white">{{ t('infiniteCanvas.title') }}</h2>
          <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-dark-400">{{ errorMessage }}</p>
          <div class="mt-6 flex flex-wrap justify-center gap-3">
            <router-link v-if="!configurationError" to="/keys" class="btn btn-primary">{{ t('infiniteCanvas.openKeys') }}</router-link>
            <button type="button" class="btn btn-secondary" @click="loadCanvas">{{ t('common.retry') }}</button>
          </div>
        </div>
      </div>

      <iframe
        v-else
        :key="iframeVersion"
        :src="canvasUrl"
        :title="t('infiniteCanvas.title')"
        class="min-h-0 flex-1 border-0"
        allow="clipboard-read; clipboard-write"
        @load="canvasReady = true"
      />
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { keysAPI } from '@/api/keys'
import { useAppStore } from '@/stores'
import type { ApiKey } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const errorMessage = ref('')
const configurationError = ref(false)
const activeKeys = ref<ApiKey[]>([])
const selectedKeyId = ref<number | null>(null)
const iframeVersion = ref(0)
const canvasReady = ref(false)

function resolveApiBaseUrl(value: string) {
  const normalized = value.trim() || '/api/v1'
  try {
    return new URL(normalized, window.location.origin).toString().replace(/\/+$/, '')
  } catch {
    return normalized.replace(/\/+$/, '')
  }
}

function maskApiKey(value: string) {
  if (value.length <= 8) return value
  return `${value.slice(0, 5)}...${value.slice(-4)}`
}
function isSelfEmbeddingUrl(value: string) {
  try {
    const target = new URL(`${value.replace(/\/+$/, '')}/`, window.location.origin)
    const currentPath = window.location.pathname.replace(/\/+$/, '') || '/'
    return target.origin === window.location.origin && target.pathname.replace(/\/+$/, '') === currentPath
  } catch {
    return false
  }
}

const selectedKey = computed(() => activeKeys.value.find((item) => item.id === selectedKeyId.value))
const apiKey = computed(() => selectedKey.value?.key || '')

const configuredCanvasUrl = computed(() => {
  return appStore.cachedPublicSettings?.infinite_canvas_url?.trim() || import.meta.env.VITE_INFINITE_CANVAS_URL?.trim() || ''
})

const canvasBaseUrl = computed(() => (configuredCanvasUrl.value || '/infinite-canvas').replace(/\/+$/, ''))

const canvasUrl = computed(() => {
  const params = new URLSearchParams({
    baseUrl: resolveApiBaseUrl(appStore.cachedPublicSettings?.api_base_url || '/api/v1'),
    apiKey: apiKey.value
  })
  return `${canvasBaseUrl.value}/?${params.toString()}`
})

async function loadCanvas() {
  loading.value = true
  errorMessage.value = ''
  configurationError.value = false
  canvasReady.value = false
  if (!configuredCanvasUrl.value && isSelfEmbeddingUrl(canvasBaseUrl.value)) {
    configurationError.value = true
    errorMessage.value = t('infiniteCanvas.notConfigured')
    loading.value = false
    return
  }
  try {
    const response = await keysAPI.list(1, 100, { status: 'active' })
    activeKeys.value = response.items.filter((item) => item.status === 'active' && item.key.trim())
    if (!activeKeys.value.length) {
      selectedKeyId.value = null
      errorMessage.value = t('infiniteCanvas.missingKey')
      return
    }
    if (!activeKeys.value.some((item) => item.id === selectedKeyId.value)) selectedKeyId.value = activeKeys.value[0].id
  } catch {
    activeKeys.value = []
    selectedKeyId.value = null
    errorMessage.value = t('infiniteCanvas.loadFailed')
  } finally {
    loading.value = false
  }
}

function handleKeyChange() {
  canvasReady.value = false
  iframeVersion.value += 1
}

async function refreshCanvas() {
  await loadCanvas()
  if (!errorMessage.value) iframeVersion.value += 1
}

function openCanvasInNewWindow() {
  window.open(canvasUrl.value, '_blank', 'noopener,noreferrer')
}

function openCodex() {
  const url = new URL(canvasUrl.value, window.location.origin)
  url.searchParams.set('mode', 'new')
  window.open(url.toString(), '_blank', 'noopener,noreferrer')
}

onMounted(() => {
  void loadCanvas()
})
</script>

<style scoped>
.canvas-toolbar {
  display: flex;
  min-height: 4.5rem;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 1px solid rgb(229 231 235 / 0.9);
  background: rgb(255 255 255 / 0.96);
  padding: 0.75rem 1rem;
}

.canvas-mark {
  display: flex;
  height: 2.25rem;
  width: 2.25rem;
  flex: none;
  align-items: center;
  justify-content: center;
  border-radius: 0.75rem;
  background: rgb(224 231 255);
  color: rgb(79 70 229);
}

.canvas-key-picker {
  display: inline-flex;
  min-width: 10rem;
}

.canvas-key-select {
  max-width: 14rem;
  min-height: 2.25rem;
  cursor: pointer;
  border: 1px solid rgb(209 213 219);
  border-radius: 0.5rem;
  background: white;
  padding: 0.375rem 2rem 0.375rem 0.75rem;
  color: rgb(17 24 39);
  font-size: 0.75rem;
  font-weight: 500;
}

.canvas-status {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  white-space: nowrap;
  color: rgb(107 114 128);
  font-size: 0.75rem;
  font-weight: 500;
}

.canvas-status-dot {
  height: 0.5rem;
  width: 0.5rem;
  border-radius: 9999px;
  background: currentColor;
}

.canvas-status--ready {
  color: rgb(22 163 74);
}

.canvas-status--connecting {
  color: rgb(217 119 6);
}

@media (prefers-color-scheme: dark) {
  .canvas-toolbar {
    border-color: rgb(55 65 81 / 0.9);
    background: rgb(17 24 39 / 0.96);
  }

  .canvas-mark {
    background: rgb(55 48 163 / 0.35);
    color: rgb(165 180 252);
  }

  .canvas-key-select {
    border-color: rgb(75 85 99);
    background: rgb(31 41 55);
    color: white;
  }
}

@media (max-width: 640px) {
  .canvas-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .canvas-toolbar > :last-child {
    width: 100%;
    justify-content: flex-start;
  }

  .canvas-key-picker,
  .canvas-key-select {
    max-width: none;
    width: 100%;
  }
}
</style>

