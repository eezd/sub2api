import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import View from '../ImageWorkbenchView.vue'
import type { WorkbenchInput, WorkbenchJob } from '@/api/imageWorkbench'

const mocks = vi.hoisted(() => ({
  keys: vi.fn(), limits: vi.fn(), models: vi.fn(), list: vi.fn(), get: vi.fn(), submit: vi.fn(),
  cancel: vi.fn(), remove: vi.fn(), drafts: vi.fn(), upload: vi.fn(), download: vi.fn(), removeAsset: vi.fn(), auth: { user: { id: 1 } }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/api/keys', () => ({ list: mocks.keys }))
vi.mock('@/api/imageWorkbench', () => ({ imageWorkbenchAPI: mocks }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))
const options = { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } }
const wrappers: ReturnType<typeof mount>[] = []
const job = (input: WorkbenchInput): WorkbenchJob => ({ id: 'job-1', api_key_id: input.api_key_id, input, status: 'running', deleting: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), items: [{ position: 0, status: 'pending', asset_id: null, error: '', duration_ms: 0 }], assets: [] })
beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear(); mocks.auth.user.id = 1
  mocks.keys.mockResolvedValue({ items: [{ id: 1, name: 'own key', status: 'active' }], pages: 1 })
  mocks.limits.mockResolvedValue({ used_bytes: 0, reserved_bytes: 0, storage_bytes: 100000000, file_bytes: 20971520, request_bytes: 100000000 })
  mocks.models.mockResolvedValue({ ids: ['test-image'], protocol: 'images', native_gemini_available: true })
  mocks.list.mockResolvedValue([]); mocks.drafts.mockResolvedValue([])
  mocks.download.mockResolvedValue(new Blob(['image'], { type: 'image/png' }))
  vi.stubGlobal('URL', class extends URL { static createObjectURL = vi.fn(() => 'blob:image'); static revokeObjectURL = vi.fn() })
})
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); vi.useRealTimers(); vi.unstubAllGlobals() })
async function ready() {
  const wrapper = mount(View, options); wrappers.push(wrapper); await flushPromises()
  expect(wrapper.get('[data-testid=image-generate]').attributes('disabled')).toBeDefined()
  await wrapper.findAll('select')[0].setValue(1); await flushPromises()
  await wrapper.findAll('select')[2].setValue('test-image')
  await wrapper.get('[data-testid=image-prompt]').setValue('Draw a bird')
  return wrapper
}
describe('Image workbench', () => {
  it('restores its key, model and Gemini protocol after remounting', async () => {
    mocks.models.mockImplementation(async (_key: number, preferred?: string) => ({ ids: ['test-image'], protocol: preferred ?? 'images', native_gemini_available: true }))
    const first = await ready()
    await first.findAll('select')[1].setValue('gemini'); await flushPromises()
    first.unmount(); wrappers.splice(wrappers.indexOf(first), 1)
    const second = mount(View, options); wrappers.push(second); await flushPromises()
    expect(second.findAll('select')[0].element.value).toBe('1')
    expect(second.findAll('select')[1].element.value).toBe('gemini')
    expect(second.findAll('select')[2].element.value).toBe('test-image')
    expect(second.get('[data-testid=image-generate]').attributes('disabled')).toBeDefined()
    expect(mocks.submit).not.toHaveBeenCalled()
    expect(JSON.stringify(localStorage)).not.toContain('Draw a bird')
  })
  it('isolates remembered selections between users', async () => {
    const first = await ready(); first.unmount(); wrappers.splice(wrappers.indexOf(first), 1)
    mocks.auth.user.id = 2
    const second = mount(View, options); wrappers.push(second); await flushPromises()
    expect(second.findAll('select')[0].element.selectedIndex).toBe(0)
    second.unmount(); wrappers.splice(wrappers.indexOf(second), 1)
    mocks.auth.user.id = 1
    const third = mount(View, options); wrappers.push(third); await flushPromises()
    expect(third.findAll('select')[0].element.value).toBe('1')
    expect(third.findAll('select')[2].element.value).toBe('test-image')
  })
  it('requires selecting again when a remembered model or key is unavailable', async () => {
    const first = await ready(); first.unmount(); wrappers.splice(wrappers.indexOf(first), 1)
    mocks.models.mockResolvedValue({ ids: ['new-image'], protocol: 'images', native_gemini_available: false })
    const second = mount(View, options); wrappers.push(second); await flushPromises()
    expect(second.findAll('select')[0].element.value).toBe('1')
    expect(second.findAll('select')[2].element.value).toBe('')
    second.unmount(); wrappers.splice(wrappers.indexOf(second), 1)
    mocks.keys.mockResolvedValue({ items: [], pages: 1 })
    const third = mount(View, options); wrappers.push(third); await flushPromises()
    expect(third.findAll('select')[0].element.selectedIndex).toBe(0)
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('keeps the saved selection when loading models temporarily fails', async () => {
    const first = await ready(); first.unmount(); wrappers.splice(wrappers.indexOf(first), 1)
    mocks.models.mockRejectedValueOnce(new Error('offline'))
    const second = mount(View, options); wrappers.push(second); await flushPromises()
    expect(second.text()).toContain('offline')
    second.unmount(); wrappers.splice(wrappers.indexOf(second), 1)
    const third = mount(View, options); wrappers.push(third); await flushPromises()
    expect(third.findAll('select')[0].element.value).toBe('1')
    expect(third.findAll('select')[2].element.value).toBe('test-image')
  })
  it('requires an explicit key and preserves submission identity after network uncertainty', async () => {
    const wrapper = await ready()
    mocks.submit.mockRejectedValueOnce({ status: 0, message: 'lost response' })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const first: WorkbenchInput = mocks.submit.mock.calls[0][0]
    expect(first.api_key_id).toBe(1); expect(first.count).toBe(1)
    expect(wrapper.get('[data-testid=image-generate]').attributes('disabled')).toBeDefined()
    const running = job(first); mocks.submit.mockResolvedValueOnce({ job: running, existing: true }); mocks.get.mockResolvedValue(running); mocks.list.mockResolvedValue([running])
    const retry = wrapper.findAll('button').find(b => b.text() === 'imageWorkbench.checkSubmission')
    await retry!.trigger('click'); await flushPromises()
    expect(mocks.submit.mock.calls[1][0]).toEqual(first)
    expect(wrapper.text()).toContain('imageWorkbench.status.running')
    wrapper.unmount(); wrappers.splice(wrappers.indexOf(wrapper), 1)
    expect(mocks.cancel).not.toHaveBeenCalled()
  })
  it('allows editing and a new submission after a definite quota rejection', async () => {
    const wrapper = await ready(); mocks.submit.mockRejectedValue({ status: 400, message: 'quota exceeded' })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('quota exceeded')
    expect(wrapper.get('[data-testid=image-generate]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid=image-prompt]').setValue('Another bird')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.submit.mock.calls[1][0].request_id).not.toBe(mocks.submit.mock.calls[0][0].request_id)
  })
  it('keeps all unused uploads visible for quota cleanup beyond the five references', async () => {
    mocks.drafts.mockResolvedValue(Array.from({ length: 6 }, (_, i) => ({ id: `a${i}`, filename: `reference-${i}.png`, mime_type: 'image/png', size: 100, width: 1, height: 1, draft: true })))
    const wrapper = mount(View, options); wrappers.push(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('reference-5.png')
    const remove = wrapper.findAll('button').find(b => b.text() === 'imageWorkbench.deleteDraft')
    await remove!.trigger('click'); await flushPromises()
    expect(mocks.removeAsset).toHaveBeenCalledWith('a5')
    expect(wrapper.text()).not.toContain('reference-5.png')
  })
  it('requires choosing a key again when the historical key is no longer available', async () => {
    const oldInput: WorkbenchInput = { request_id: 'old', api_key_id: 99, protocol: 'images', model: 'test-image', prompt: 'history', count: 1, size: 'auto', quality: 'auto', aspect_ratio: '', image_size: '', reference_ids: [] }
    const old = { ...job(oldInput), status: 'failed' as const }
    mocks.list.mockResolvedValue([old]); mocks.get.mockResolvedValue(old)
    const wrapper = await ready()
    expect(wrapper.findAll('select')[0].element.value).toBe('1')
    const reuse = wrapper.findAll('button').find(b => b.text() === 'imageWorkbench.reuse')
    await reuse!.trigger('click'); await flushPromises()
    expect(wrapper.findAll('select')[0].element.selectedIndex).toBe(0)
    expect(wrapper.get('[data-testid=image-generate]').attributes('disabled')).toBeDefined()
    expect(mocks.submit).not.toHaveBeenCalled()
  })
  it('reloads a running task and observes its terminal state without submitting again', async () => {
    const input: WorkbenchInput = { request_id: 'id', api_key_id: 1, protocol: 'gemini', model: 'test-image', prompt: 'old task', count: 1, size: '', quality: '', aspect_ratio: 'auto', image_size: 'auto', reference_ids: [] }
    const running = job(input); mocks.list.mockResolvedValue([running]); mocks.get.mockResolvedValueOnce(running)
    vi.useFakeTimers()
    const wrapper = mount(View, options); wrappers.push(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('old task')
    const completed = { ...running, status: 'failed' as const, items: [{ ...running.items[0], status: 'failed' as const, error: 'provider error' }] }
    mocks.get.mockResolvedValue(completed); mocks.list.mockResolvedValue([completed])
    await vi.advanceTimersByTimeAsync(3000); await flushPromises()
    expect(wrapper.text()).toContain('provider error'); expect(wrapper.text()).toContain('imageWorkbench.status.failed')
    expect(mocks.submit).not.toHaveBeenCalled()
  })
})
