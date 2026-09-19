<template>
  <div class="relative min-w-[8rem]" :aria-label="t('admin.accounts.recentRequests.summary')">
    <div v-if="displayRequests.length" class="flex items-center gap-1" data-testid="recent-request-bars">
      <button
        v-for="(request, index) in displayRequests"
        :key="requestKey(request, index)"
        :ref="(element) => setTriggerRef(element as Element | null, index)"
        type="button"
        class="group flex h-8 w-5 items-center justify-center rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 focus-visible:ring-offset-1 dark:focus-visible:ring-offset-dark-800"
        :aria-label="requestAriaLabel(request)"
        :aria-expanded="activeIndex === index"
      @mouseenter="openRequest(index, false)"
        @mouseleave="scheduleClose"
        @blur="handleTriggerBlur"
        @focus="handleTriggerFocus(index)"
        @click="togglePinned(index)"
        @keydown.esc.prevent.stop="closePanel(true)"
      >
        <span
          :class="[
            'h-5 w-1.5 rounded-full shadow-sm transition-transform duration-150 group-hover:scale-y-110 motion-reduce:transition-none',
            request.kind === 'error'
              ? 'bg-red-500 shadow-red-500/20 dark:bg-red-400'
              : 'bg-emerald-500 shadow-emerald-500/20 dark:bg-emerald-400'
          ]"
        />
      </button>
      <span
        v-if="stale"
        class="ml-1 inline-flex text-amber-500 dark:text-amber-400"
        :title="t('admin.accounts.recentRequests.stale')"
        :aria-label="t('admin.accounts.recentRequests.stale')"
        data-testid="recent-requests-stale"
      >
        <Icon name="exclamationTriangle" size="xs" />
      </span>
    </div>

    <div v-else-if="loading" class="flex h-8 items-center gap-1" aria-busy="true">
      <span v-for="index in 5" :key="index" class="h-5 w-1.5 animate-pulse rounded-full bg-gray-200 motion-reduce:animate-none dark:bg-dark-600" />
    </div>
    <span v-else-if="loadError" class="text-xs text-red-500 dark:text-red-400">
      {{ t('admin.accounts.recentRequests.loadFailed') }}
    </span>
    <span v-else class="text-sm text-gray-400 dark:text-dark-500" :aria-label="t('admin.accounts.recentRequests.empty')">—</span>

    <Teleport to="body">
      <div
        v-if="activeRequest"
        ref="panelRef"
        role="dialog"
        :aria-label="t('admin.accounts.recentRequests.viewDetails')"
        tabindex="-1"
        class="fixed z-[100] overscroll-contain overflow-y-auto rounded-xl border border-gray-200 bg-white p-4 shadow-2xl shadow-gray-900/15 dark:border-dark-600 dark:bg-dark-800 dark:shadow-black/40"
        :style="panelStyle"
        @mouseenter="cancelClose"
        @mouseleave="scheduleClose"
      >
        <button
          ref="closeButtonRef"
          type="button"
          class="absolute right-3 top-3 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-dark-700 dark:hover:text-gray-200"
          :aria-label="t('common.close')"
          @click="closePanel(true)"
          @keydown.esc.prevent.stop="closePanel(true)"
        >
          <Icon name="x" size="sm" />
        </button>
        <RecentRequestDetails
          class="pr-7"
          :request="activeRequest"
          :account-id="accountId"
          :account-name="accountName"
        />
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpsRequestDetail } from '@/api/admin/ops'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'
import RecentRequestDetails from './RecentRequestDetails.vue'

const props = withDefaults(defineProps<{
  accountId: number
  accountName: string
  requests: OpsRequestDetail[]
  loading?: boolean
  loadError?: boolean
  stale?: boolean
}>(), {
  loading: false,
  loadError: false,
  stale: false
})

const { t } = useI18n()
const displayRequests = computed(() => props.requests.slice(0, 5).reverse())
const activeIndex = ref<number | null>(null)
const pinned = ref(false)
const panelRef = ref<HTMLElement | null>(null)
const closeButtonRef = ref<HTMLButtonElement | null>(null)
const triggerRefs = new Map<number, HTMLElement>()
let closeTimer: ReturnType<typeof setTimeout> | null = null
let listening = false

let suppressNextFocusOpen = false
const panelPosition = reactive({
  top: null as number | null,
  bottom: null as number | null,
  left: 16,
  width: 360,
  maxHeight: 0
})

const activeRequest = computed(() => (
  activeIndex.value == null ? null : displayRequests.value[activeIndex.value] ?? null
))

const panelStyle = computed(() => ({
  top: panelPosition.top == null ? 'auto' : `${panelPosition.top}px`,
  bottom: panelPosition.bottom == null ? 'auto' : `${panelPosition.bottom}px`,
  left: `${panelPosition.left}px`,
  width: `${panelPosition.width}px`,
  maxHeight: `${panelPosition.maxHeight}px`
}))

const requestIdentity = (request: OpsRequestDetail): string => (
  `${request.kind}:${request.kind === 'error' ? (request.error_id ?? request.request_id) : request.request_id}:${request.created_at}`
)

const requestKey = (request: OpsRequestDetail, index: number): string => (
  `${requestIdentity(request)}:${index}`
)

const requestAriaLabel = (request: OpsRequestDetail): string => {
  const status = request.kind === 'error'
    ? t('admin.accounts.recentRequests.error')
    : t('admin.accounts.recentRequests.success')
  return `${status} · ${formatDateTime(request.created_at)}`
}

const setTriggerRef = (element: Element | null, index: number) => {
  if (element instanceof HTMLElement) triggerRefs.set(index, element)
  else triggerRefs.delete(index)
}

const updatePosition = () => {
  if (activeIndex.value == null) return
  const trigger = triggerRefs.get(activeIndex.value)
  if (!trigger) return
  Object.assign(panelPosition, getFloatingPanelPosition(
    trigger.getBoundingClientRect(),
    document.documentElement.clientWidth || window.innerWidth,
    window.innerHeight,
    { maxWidth: 360, maxHeightRatio: 0.78 }
  ))
}

const handleViewportChange = () => updatePosition()

const startListening = () => {
  if (listening) return
  window.addEventListener('resize', handleViewportChange)
  window.addEventListener('scroll', handleViewportChange, true)
  document.addEventListener('pointerdown', handleOutsidePointerDown)
  listening = true
}

const stopListening = () => {
  if (!listening) return
  window.removeEventListener('resize', handleViewportChange)
  window.removeEventListener('scroll', handleViewportChange, true)
  document.removeEventListener('pointerdown', handleOutsidePointerDown)
  listening = false
}

const cancelClose = () => {
  if (closeTimer !== null) {
    clearTimeout(closeTimer)
    closeTimer = null
  }
}

const closePanel = (restoreFocus = false) => {
  cancelClose()
  const trigger = activeIndex.value == null ? null : triggerRefs.get(activeIndex.value)
  activeIndex.value = null
  pinned.value = false
  stopListening()
  if (restoreFocus && trigger) {
    void nextTick(() => {
      if (document.activeElement === trigger) return
      suppressNextFocusOpen = true
      trigger.focus()
    })
  }
}

const scheduleClose = () => {
  if (pinned.value) return
  cancelClose()
  closeTimer = setTimeout(() => closePanel(false), 120)
}

const openRequest = (index: number, allowPinnedSwitch = true) => {
  if (pinned.value && !allowPinnedSwitch) return
  cancelClose()
  activeIndex.value = index
  startListening()
  void nextTick(updatePosition)
}

const handleTriggerFocus = (index: number) => {
  if (suppressNextFocusOpen) {
    suppressNextFocusOpen = false
    return
  }
  openRequest(index)
}

const togglePinned = (index: number) => {
  if (activeIndex.value === index && pinned.value) {
    closePanel(true)
    return
  }
  openRequest(index)
  pinned.value = true
  void nextTick(() => closeButtonRef.value?.focus())
}

const handleTriggerBlur = (event: FocusEvent) => {
  const nextTarget = event.relatedTarget
  if (nextTarget instanceof Node && panelRef.value?.contains(nextTarget)) return
  scheduleClose()
}

function handleOutsidePointerDown(event: PointerEvent) {
  const target = event.target
  if (!(target instanceof Node)) return
  if (panelRef.value?.contains(target)) return
  if (activeIndex.value != null && triggerRefs.get(activeIndex.value)?.contains(target)) return
  closePanel(false)
}

watch(displayRequests, (requests, previousRequests) => {
  if (activeIndex.value == null) return
  const previousRequest = previousRequests[activeIndex.value]
  if (!previousRequest) {
    closePanel(false)
    return
  }
  const nextIndex = requests.findIndex(request => requestIdentity(request) === requestIdentity(previousRequest))
  if (nextIndex < 0) {
    closePanel(false)
    return
  }
  activeIndex.value = nextIndex
  void nextTick(updatePosition)
})


onBeforeUnmount(() => {
  closePanel(false)
  triggerRefs.clear()
})
</script>
