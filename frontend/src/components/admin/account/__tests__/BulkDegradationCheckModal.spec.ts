import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BulkDegradationCheckModal from '../BulkDegradationCheckModal.vue'

const api = vi.hoisted(() => ({
  getById: vi.fn(), getAvailableModels: vi.fn(), createDegradationCheckBatch: vi.fn(),
  listDegradationCheckBatches: vi.fn(), getDegradationCheckBatch: vi.fn(),
  listDegradationCheckBatchItems: vi.fn(), getDegradationCheckBatchItem: vi.fn(), cancelDegradationCheckBatch: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: api } }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key}${params ? JSON.stringify(params) : ''}` }) }))
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}
const selectStub = {
  props: ['modelValue', 'options', 'disabled'],
  emits: ['update:modelValue'],
  template: `<select :value="modelValue || ''" :disabled="disabled" @change="$emit('update:modelValue', $event.target.value || null)"><option value="">Select</option><option v-for="option in options" :value="option.value">{{ option.label }}</option></select>`
}
function open(ids: number[]) {
  return mount(BulkDegradationCheckModal, {
    props: { show: true, accountIds: ids, checkType: 'model_trace' },
    global: { stubs: {
      BaseDialog: { props: ['show'], emits: ['close'], template: '<div v-if="show"><slot/><slot name="footer"/><button data-testid="dialog-close" @click="$emit(\'close\')">Close</button></div>' },
      Select: selectStub, Pagination: true, ConfirmDialog: { props: ['show'], emits: ['confirm', 'cancel'], template: '<div v-if="show" data-testid="cancel-confirmation"><button data-testid="confirm-cancel" @click="$emit(\'confirm\')">Confirm</button><button data-testid="dismiss-cancel" @click="$emit(\'cancel\')">Dismiss</button></div>' }, ModelTraceResultPanel: true, SvgAnimationResultPreview: true
    } }
  })
}
const model = (id: string) => ({ id, display_name: id, type: 'model', created: 0 })

describe('bulk degradation preparation and persisted submission', () => {
  beforeEach(() => {
    Object.values(api).forEach(mock => mock.mockReset())
    Object.defineProperty(document, 'hidden', { configurable: true, value: false })
    api.getById.mockImplementation(async (id: number) => ({ id, name: `Account ${id}`, platform: 'openai', type: 'apikey' }))
    api.getAvailableModels.mockResolvedValue([model('shared')])
    api.listDegradationCheckBatches.mockResolvedValue({ items: [], total: 0 })
    api.getDegradationCheckBatch.mockImplementation(async (id: number) => ({ id, check_type: 'model_trace', status: 'completed', counts: { total: 1, pending: 0, running: 0, succeeded: 1, failed: 0, skipped: 0, canceled: 0, interrupted: 0 } }))
    api.listDegradationCheckBatchItems.mockResolvedValue({ items: [], total: 0 })
  })
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })
  it('prepares every cross-page ID with four concurrent rows and never defaults models', async () => {
    const pending = Array.from({ length: 6 }, () => deferred<unknown>())
    api.getById.mockImplementation((id: number) => pending[id - 1]!.promise)
    const wrapper = open([1, 2, 3, 4, 5, 6])
    await flushPromises()
    expect(api.getById).toHaveBeenCalledTimes(4)
    expect(wrapper.findAll('[data-account-id]')).toHaveLength(6)
    pending.slice(0, 4).forEach((promise, index) => promise.resolve({ id: index + 1, name: `A${index}`, platform: 'openai', type: 'apikey' }))
    await flushPromises()
    expect(api.getById).toHaveBeenCalledTimes(6)
    pending.slice(4).forEach((promise, index) => promise.resolve({ id: index + 5, name: `A${index + 4}`, platform: 'openai', type: 'apikey' }))
    await flushPromises()
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('[data-account-id] select').every(select => (select.element as HTMLSelectElement).value === '')).toBe(true)
    wrapper.unmount()
  })
  it('fills only matching per-account catalogs and submits skipped rows explicitly as null', async () => {
    api.getAvailableModels.mockImplementation(async (id: number) => id === 1 ? [model('shared')] : id === 2 ? [model('other')] : [])
    const wrapper = open([1, 2, 3])
    await flushPromises()
    await wrapper.findAll('select')[0]!.setValue('shared')
    const apply = wrapper.findAll('button').find(button => button.text().includes('applyPlatform'))!
    await apply.trigger('click')
    expect((wrapper.get('[data-account-id="1"] select').element as HTMLSelectElement).value).toBe('shared')
    expect((wrapper.get('[data-account-id="2"] select').element as HTMLSelectElement).value).toBe('')
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-account-id="2"] select').setValue('other')
    const submitted = deferred<unknown>()
    api.createDegradationCheckBatch.mockReturnValue(submitted.promise)
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    expect(api.createDegradationCheckBatch.mock.calls[0]![0].items).toEqual([{ account_id: 1, model_id: 'shared' }, { account_id: 2, model_id: 'other' }, { account_id: 3, model_id: null }])
    expect(wrapper.find('fieldset').attributes('disabled')).toBeDefined()
    await wrapper.setProps({ accountIds: [999] })
    expect(api.createDegradationCheckBatch.mock.calls[0]![0].items[0].account_id).toBe(1)
    submitted.resolve({ id: 20 })
    await flushPromises()
    expect(wrapper.text()).toContain('#20')
    wrapper.unmount()
  })
  it('reuses the explicit submission key on unchanged manual retry but changes it with configuration', async () => {
    api.getAvailableModels.mockResolvedValue([model('a'), model('b')])
    api.createDegradationCheckBatch.mockRejectedValue(new Error('response lost'))
    const wrapper = open([1])
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('a')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    const key = api.createDegradationCheckBatch.mock.calls[0]![1]
    expect(wrapper.get('[role="alert"]').text()).toContain('unknownSubmission')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(api.createDegradationCheckBatch.mock.calls[1]![1]).toBe(key)
    await wrapper.get('[data-account-id="1"] select').setValue('b')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(api.createDegradationCheckBatch.mock.calls[2]![1]).not.toBe(key)
    wrapper.unmount()
  })
  it('falls back when randomUUID is unavailable and preserves request identity across an unknown manual retry', async () => {
    vi.stubGlobal('crypto', {})
    api.createDegradationCheckBatch.mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce({ id: 21 })
    const wrapper = open([1])
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('shared')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    const key = api.createDegradationCheckBatch.mock.calls[0]![1]
    expect(key).toEqual(expect.any(String))
    expect(key).not.toBe('')
    expect(wrapper.get('[role="alert"]').text()).toContain('unknownSubmission')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(api.createDegradationCheckBatch.mock.calls[1]![1]).toBe(key)
    expect(wrapper.text()).toContain('#21')
    wrapper.unmount()
  })
  it('keeps an in-flight submission locked and reuses its key after close and reopen', async () => {
    const submitted = deferred<unknown>()
    api.createDegradationCheckBatch.mockReturnValueOnce(submitted.promise).mockResolvedValueOnce({ id: 22 })
    const wrapper = open([1])
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('shared')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    const key = api.createDegradationCheckBatch.mock.calls[0]![1]

    await wrapper.get('[data-testid="dialog-close"]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    expect(api.createDegradationCheckBatch).toHaveBeenCalledTimes(1)

    submitted.reject(new Error('response lost'))
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('shared')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await flushPromises()
    expect(api.createDegradationCheckBatch).toHaveBeenCalledTimes(2)
    expect(api.createDegradationCheckBatch.mock.calls[1]![1]).toBe(key)
    expect(wrapper.text()).toContain('#22')
    wrapper.unmount()
  })

  it('does not attach a failed cancellation to a different batch view', async () => {
    const cancellation = deferred<unknown>()
    const runningBatch = (id: number) => ({ id, check_type: 'model_trace', status: 'running', created_at: 'now', counts: { total: 1, pending: 0, running: 1, succeeded: 0, failed: 0, skipped: 0, canceled: 0, interrupted: 0 } })
    api.listDegradationCheckBatches.mockResolvedValue({ items: [runningBatch(200), runningBatch(201)], total: 2 })
    api.getDegradationCheckBatch.mockImplementation(async (id: number) => runningBatch(id))
    api.cancelDegradationCheckBatch.mockReturnValue(cancellation.promise)
    const wrapper = open([])
    await flushPromises()
    await wrapper.get('[data-batch-id="200"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="batch-cancel"]').trigger('click')
    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click')
    await wrapper.get('[data-testid="batch-tasks"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-batch-id="201"]').trigger('click')
    await flushPromises()

    cancellation.reject(new Error('cancel A failed'))
    await flushPromises()
    expect(wrapper.text()).toContain('#201')
    expect(wrapper.text()).not.toContain('cancel A failed')
    wrapper.unmount()
  })
  it('cancels the batch captured by confirmation when a late create response opens another batch', async () => {
    const submitted = deferred<unknown>()
    const runningBatch = { id: 200, check_type: 'model_trace', status: 'running', created_at: 'now', counts: { total: 1, pending: 0, running: 1, succeeded: 0, failed: 0, skipped: 0, canceled: 0, interrupted: 0 } }
    api.createDegradationCheckBatch.mockReturnValue(submitted.promise)
    api.listDegradationCheckBatches.mockResolvedValue({ items: [runningBatch], total: 1 })
    api.getDegradationCheckBatch.mockImplementation(async (id: number) => id === 200 ? runningBatch : { ...runningBatch, id })
    api.cancelDegradationCheckBatch.mockResolvedValue(undefined)
    const wrapper = open([1])
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('shared')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    const tasks = wrapper.findAll('button').find(button => button.text().includes('degradationBatch.tasks'))!
    await tasks.trigger('click')
    await flushPromises()
    await wrapper.get('[data-batch-id="200"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="batch-cancel"]').trigger('click')
    expect(wrapper.find('[data-testid="cancel-confirmation"]').exists()).toBe(true)
    submitted.resolve({ id: 123 })
    await flushPromises()
    expect(wrapper.text()).toContain('#123')
    expect(wrapper.find('[data-testid="cancel-confirmation"]').exists()).toBe(true)
    await wrapper.get('[data-testid="confirm-cancel"]').trigger('click')
    await flushPromises()
    expect(api.cancelDegradationCheckBatch).toHaveBeenCalledTimes(1)
    expect(api.cancelDegradationCheckBatch).toHaveBeenCalledWith(200)
    wrapper.unmount()
  })
  it('does not cancel a creating task on close and ignores its late response after another preparation opens', async () => {
    const submitted = deferred<unknown>()
    api.createDegradationCheckBatch.mockReturnValue(submitted.promise)
    const wrapper = open([1])
    await flushPromises()
    await wrapper.get('[data-account-id="1"] select').setValue('shared')
    await wrapper.get('[data-testid="batch-start"]').trigger('click')
    await wrapper.get('[data-testid="dialog-close"]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, accountIds: [2] })
    await flushPromises()
    submitted.resolve({ id: 123 })
    await flushPromises()
    expect(wrapper.find('[data-account-id="2"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('#123')
    expect(api.cancelDegradationCheckBatch).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('ignores late preparation from an earlier open and leaves account errors reloadable rather than deleted', async () => {
    const old = deferred<unknown>()
    api.getById.mockImplementation((id: number) => id === 1 ? old.promise : Promise.reject({ response: { status: 503 } }))
    const wrapper = open([1])
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, accountIds: [2] })
    await flushPromises()
    old.resolve({ id: 1, name: 'Old account', platform: 'openai', type: 'apikey' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Old account')
    expect(wrapper.text()).toContain('accountError')
    expect(wrapper.text()).not.toContain('deleted')
    expect(wrapper.get('[data-testid="batch-start"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
