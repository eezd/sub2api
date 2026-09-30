import { onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { accountsAPI, type DegradationBatch, type DegradationBatchItem, type DegradationBatchItemDetail } from '@/api/admin/accounts'

export interface DegradationCheckBatchesView {
  batches: Ref<DegradationBatch[]>
  batch: Ref<DegradationBatch | null>
  items: Ref<DegradationBatchItem[]>
  detail: Ref<DegradationBatchItemDetail | null>
  batchId: Ref<number | null>
  page: Ref<number>
  itemPage: Ref<number>
  total: Ref<number>
  itemTotal: Ref<number>
  loading: Ref<boolean>
  stale: Ref<boolean>
  error: Ref<string>
  refresh: () => Promise<void>
  openBatch: (id: number | null) => void
  openItem: (id: number | null) => void
  changePage: (page: number) => void
  terminal: (batch: DegradationBatch) => boolean
}

export function useDegradationCheckBatches(visible: Ref<boolean>): DegradationCheckBatchesView {
  const batches = ref<DegradationBatch[]>([])
  const batch = ref<DegradationBatch | null>(null)
  const items = ref<DegradationBatchItem[]>([])
  const detail = ref<DegradationBatchItemDetail | null>(null)
  const batchId = ref<number | null>(null)
  const itemId = ref<number | null>(null)
  const page = ref(1)
  const itemPage = ref(1)
  const total = ref(0)
  const itemTotal = ref(0)
  const loading = ref(false)
  const stale = ref(false)
  const error = ref('')
  let generation = 0
  let controller: AbortController | null = null
  let timer: number | undefined
  const terminal = (value: DegradationBatch) => value.status === 'completed' || value.status === 'canceled'
  const active = () => visible.value && !document.hidden
  function stop() {
    generation++
    clearTimeout(timer)
    controller?.abort()
    controller = null
    loading.value = false
  }
  async function refresh() {
    stop()
    if (!active()) return
    const token = generation
    const request = new AbortController()
    controller = request
    loading.value = true
    try {
      const id = batchId.value
      if (id === null) {
        const response = await accountsAPI.listDegradationCheckBatches({ page: page.value, page_size: 10 }, request.signal)
        if (token !== generation) return
        batches.value = response.items
        total.value = response.total
      } else {
        const [summary, rows, result] = await Promise.all([
          accountsAPI.getDegradationCheckBatch(id, request.signal),
          accountsAPI.listDegradationCheckBatchItems(id, { page: itemPage.value, page_size: 50 }, request.signal),
          itemId.value === null ? Promise.resolve(null) : accountsAPI.getDegradationCheckBatchItem(id, itemId.value, request.signal)
        ])
        if (token !== generation) return
        batch.value = summary
        items.value = rows.items
        itemTotal.value = rows.total
        detail.value = result
      }
      stale.value = false
      error.value = ''
      const nonterminal = batchId.value === null
        ? batches.value.some(value => !terminal(value))
        : (batch.value !== null && !terminal(batch.value)) || items.value.some(value => value.status === 'pending' || value.status === 'running')
      if (active() && nonterminal) timer = window.setTimeout(() => void refresh(), 2000)
    } catch (cause) {
      if (token !== generation || request.signal.aborted) return
      request.abort()
      stale.value = true
      error.value = cause && typeof cause === 'object' && 'message' in cause && typeof cause.message === 'string' ? cause.message : String(cause)
    } finally {
      if (token === generation) {
        loading.value = false
        controller = null
      }
    }
  }
  function openBatch(id: number | null) {
    stop()
    batchId.value = id
    itemId.value = null
    itemPage.value = 1
    batch.value = null
    items.value = []
    detail.value = null
    void refresh()
  }
  function openItem(id: number | null) {
    detail.value = null
    itemId.value = id
    void refresh()
  }
  function changePage(value: number) {
    if (batchId.value === null) page.value = value
    else {
      itemPage.value = value
      itemId.value = null
      detail.value = null
    }
    void refresh()
  }
  const visibilityChanged = () => { if (active()) void refresh(); else stop() }
  watch(visible, visibilityChanged, { immediate: true })
  onMounted(() => document.addEventListener('visibilitychange', visibilityChanged))
  onUnmounted(() => {
    stop()
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return { batches, batch, items, detail, batchId, page, itemPage, total, itemTotal, loading, stale, error, refresh, openBatch, openItem, changePage, terminal }
}
