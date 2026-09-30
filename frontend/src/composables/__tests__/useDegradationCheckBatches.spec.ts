import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useDegradationCheckBatches, type DegradationCheckBatchesView } from '../useDegradationCheckBatches'

const api = vi.hoisted(() => ({
  listDegradationCheckBatches: vi.fn(), getDegradationCheckBatch: vi.fn(),
  listDegradationCheckBatchItems: vi.fn(), getDegradationCheckBatchItem: vi.fn(), cancelDegradationCheckBatch: vi.fn()
}))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: api }))
const task = (id: number, status = 'running') => ({ id, status, counts: { total: 1, pending: 0, running: 1, succeeded: 0, failed: 0, skipped: 0, canceled: 0, interrupted: 0 } })
const batchItem = (status: 'running' | 'succeeded') => ({
  id: 11, position: 1, account_id: 42, account_name: 'saved account', platform: 'openai', account_type: 'standard',
  requested_model: 'gpt-5.4', tested_model: 'gpt-5.4', status, reason_code: '', error_message: '', progress: {},
  history_id: status === 'succeeded' ? 71 : null, started_at: '2026-09-30T10:00:00Z',
  finished_at: status === 'succeeded' ? '2026-09-30T10:01:00Z' : null, updated_at: '2026-09-30T10:01:00Z',
  model_trace_summary: status === 'succeeded' ? { matches_expected: true, prediction_name: 'gpt-5.4', probability: 0.98 } : null
})
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
function harness() {
  const visible = ref(true)
  let query!: DegradationCheckBatchesView
  const wrapper = mount(defineComponent({ setup() { query = useDegradationCheckBatches(visible); return () => null } }))
  return { visible, query, wrapper }
}

describe('persisted degradation task queries', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    Object.values(api).forEach(mock => mock.mockReset())
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    api.listDegradationCheckBatches.mockResolvedValue({ items: [task(1)], total: 1 })
    api.getDegradationCheckBatch.mockImplementation(async (id: number) => task(id))
    api.listDegradationCheckBatchItems.mockResolvedValue({ items: [], total: 0 })
  })
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })
  it('does not overlap polling and stops after a terminal response', async () => {
    const pending = deferred<{ items: unknown[]; total: number }>()
    api.listDegradationCheckBatches.mockReturnValueOnce(pending.promise)
    const { query, wrapper } = harness()
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(1)
    pending.resolve({ items: [task(1)], total: 1 })
    await flushPromises()
    api.listDegradationCheckBatches.mockResolvedValue({ items: [task(1, 'completed')], total: 1 })
    await vi.advanceTimersByTimeAsync(2000)
    expect(query.batches.value[0]?.status).toBe('completed')
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
  it.each(['completed', 'canceled'] as const)('keeps polling a %s batch until displayed running rows expose their saved result', async (summaryStatus) => {
    const saved = batchItem('succeeded')
    api.getDegradationCheckBatch.mockResolvedValue(task(1, summaryStatus))
    api.listDegradationCheckBatchItems
      .mockResolvedValueOnce({ items: [batchItem('running')], total: 1 })
      .mockResolvedValue({ items: [saved], total: 1 })
    api.getDegradationCheckBatchItem.mockResolvedValue({ ...saved, result: { requested_model: 'gpt-5.4', tested_model: 'gpt-5.4' }, output_text: 'saved output' })
    const { query, wrapper } = harness()
    await flushPromises()

    query.openBatch(1)
    await flushPromises()
    expect(query.batch.value?.status).toBe(summaryStatus)
    expect(query.items.value[0]?.status).toBe('running')

    await vi.advanceTimersByTimeAsync(2000)
    expect(query.items.value[0]).toMatchObject({
      status: 'succeeded',
      history_id: 71,
      model_trace_summary: { prediction_name: 'gpt-5.4', probability: 0.98 }
    })
    expect(api.listDegradationCheckBatchItems).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(10000)
    expect(api.listDegradationCheckBatchItems).toHaveBeenCalledTimes(2)

    query.openItem(11)
    await flushPromises()
    expect(query.detail.value).toMatchObject({ history_id: 71, result: { tested_model: 'gpt-5.4' }, output_text: 'saved output' })
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.listDegradationCheckBatchItems).toHaveBeenCalledTimes(3)
    wrapper.unmount()
  })
  it('aborts old GETs and rejects late responses when switching batch', async () => {
    const old = deferred<unknown>()
    const { query, wrapper } = harness()
    await flushPromises()
    api.getDegradationCheckBatch.mockImplementation((id: number) => id === 1 ? old.promise : Promise.resolve(task(id)))
    query.openBatch(1)
    const signal = api.getDegradationCheckBatch.mock.calls[0]?.[1] as AbortSignal
    query.openBatch(2)
    await flushPromises()
    expect(signal.aborted).toBe(true)
    old.resolve(task(1))
    await flushPromises()
    expect(query.batch.value?.id).toBe(2)
    wrapper.unmount()
  })
  it('preserves last progress on errors and resumes only after manual refresh', async () => {
    const { query, wrapper } = harness()
    await flushPromises()
    api.listDegradationCheckBatches.mockRejectedValueOnce(new Error('offline'))
    await vi.advanceTimersByTimeAsync(2000)
    expect(query.stale.value).toBe(true)
    expect(query.batches.value[0]?.id).toBe(1)
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(2)
    await query.refresh()
    expect(query.stale.value).toBe(false)
    await vi.advanceTimersByTimeAsync(2000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(4)
    wrapper.unmount()
  })
  it('closing and document hiding stop GETs without canceling execution, and reentry queries persisted tasks', async () => {
    const { visible, query, wrapper } = harness()
    await flushPromises()
    visible.value = false
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(1)
    visible.value = true
    await flushPromises()
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(2)
    Object.defineProperty(document, 'hidden', { configurable: true, value: true })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.listDegradationCheckBatches).toHaveBeenCalledTimes(2)
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(query.batches.value[0]?.id).toBe(1)
    wrapper.unmount()
    const reentry = harness()
    await flushPromises()
    expect(reentry.query.batches.value[0]?.id).toBe(1)
    expect(api.cancelDegradationCheckBatch).not.toHaveBeenCalled()
    reentry.wrapper.unmount()
  })
})
