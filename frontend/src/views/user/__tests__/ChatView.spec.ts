import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import ChatView from '../ChatView.vue'
import type { ChatDetail, ChatMessage } from '@/api/chat'

const mocks = vi.hoisted(() => ({
  api: { list: vi.fn(), create: vi.fn(), get: vi.fn(), rename: vi.fn(), remove: vi.fn(), limits: vi.fn(), upload: vi.fn(), download: vi.fn(), removeAttachment: vi.fn(), start: vi.fn(), save: vi.fn() },
  keys: vi.fn(), key: vi.fn(), models: vi.fn(), stream: vi.fn(), auth: { user: { id: 1 } }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/api/chat', async () => ({ ...await vi.importActual<typeof import('@/api/chat')>('@/api/chat'), chatAPI: mocks.api, chatModels: mocks.models, streamChat: mocks.stream }))
vi.mock('@/api/keys', () => ({ list: mocks.keys, getById: mocks.key }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const conversation = { id: 'c', title: 'Saved conversation', api_key_id: 10, model: 'model', deleting: false, updated_at: new Date().toISOString() }
const key = { id: 10, name: 'My key', key: 'KEY_MUST_STAY_PRIVATE', status: 'active', user_id: 1 }
const limits = { used_bytes: 0, storage_bytes: 100000000, file_bytes: 20971520, text_bytes: 2000000, request_bytes: 95000000 }
let wrapper: ReturnType<typeof mount>, router: ReturnType<typeof createRouter>
async function setup(path = '/chat') {
  router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/chat/:id?', component: ChatView }, { path: '/other', component: { template: '<p>other</p>' } }] })
  await router.push(path); await router.isReady()
  wrapper = mount({ template: '<router-view />' }, { global: { plugins: [router] } }); await flushPromises()
}
async function ready() {
  await wrapper.get('[data-testid="chat-key"]').setValue(10); await flushPromises()
  await wrapper.get('[data-testid="chat-model"]').setValue('model')
  await wrapper.get('[data-testid="chat-input"]').setValue('hello')
}
beforeEach(() => {
  vi.clearAllMocks(); localStorage.clear()
  mocks.auth.user.id = 1
  mocks.api.list.mockResolvedValue([conversation]); mocks.api.create.mockResolvedValue(conversation)
  mocks.api.get.mockImplementation(async (): Promise<ChatDetail> => ({ conversation: { ...conversation }, messages: [], attachments: [] }))
  mocks.api.limits.mockResolvedValue(limits); mocks.api.save.mockResolvedValue(undefined)
  mocks.api.start.mockResolvedValue({ user_message_id: 'u', assistant_message_id: 'a', existing: false })
  mocks.keys.mockResolvedValue({ items: [key], pages: 1 }); mocks.key.mockResolvedValue(key); mocks.models.mockResolvedValue(['model'])
  mocks.stream.mockImplementation(async (_key: string, _cid: string, _body: string, _signal: AbortSignal, delta: (text: string, reasoning: string) => void) => { delta('answer', 'thought') })
})
afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })

describe('ChatView', () => {
  it.each(['invalid-json', '{"keyID":"10","model":"model"}', '{"keyID":10,"model":"model","protocol":"unknown"}'])('reports invalid stored preferences without preventing a fresh selection: %s', async raw => {
    localStorage.setItem('sub2api:chat:selection:1', raw)
    await setup()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).selectedIndex).toBe(0)
    expect(wrapper.get('[role="alert"]').text()).not.toBe('')
    await ready()
    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeUndefined()
  })
  it('keeps the latest key/model after navigation, including an existing conversation', async () => {
    mocks.models.mockResolvedValue(['model', 'chosen-model'])
    await setup('/chat/c')
    await wrapper.get('[data-testid="chat-model"]').setValue('chosen-model')
    await router.push('/other'); await flushPromises()
    await router.push('/chat/c'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).value).toBe('10')
    expect((wrapper.get('[data-testid="chat-model"]').element as HTMLSelectElement).value).toBe('chosen-model')
    expect(mocks.stream).not.toHaveBeenCalled()
    expect(JSON.stringify(localStorage)).not.toContain(key.key)
  })
  it('isolates remembered selections between users', async () => {
    await setup(); await ready()
    await router.push('/other'); await flushPromises()
    mocks.auth.user.id = 2
    await router.push('/chat'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).selectedIndex).toBe(0)
    await router.push('/other'); await flushPromises()
    mocks.auth.user.id = 1
    await router.push('/chat'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-model"]').element as HTMLSelectElement).value).toBe('model')
  })
  it('does not restore a disabled key or an unavailable model', async () => {
    mocks.models.mockResolvedValue(['model', 'another'])
    await setup(); await ready()
    await router.push('/other'); await flushPromises()
    mocks.models.mockResolvedValue(['replacement'])
    await router.push('/chat'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).value).toBe('10')
    expect((wrapper.get('[data-testid="chat-model"]').element as HTMLSelectElement).value).toBe('')
    await router.push('/other'); await flushPromises()
    mocks.keys.mockResolvedValue({ items: [{ ...key, status: 'disabled' }], pages: 1 })
    await router.push('/chat'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).selectedIndex).toBe(0)
    expect(mocks.stream).not.toHaveBeenCalled()
  })
  it('keeps the saved selection when loading models temporarily fails', async () => {
    await setup(); await ready()
    await router.push('/other'); await flushPromises()
    mocks.models.mockRejectedValueOnce(new Error('offline'))
    await router.push('/chat'); await flushPromises()
    expect(wrapper.text()).toContain('offline')
    await router.push('/other'); await flushPromises()
    await router.push('/chat'); await flushPromises()
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).value).toBe('10')
    expect((wrapper.get('[data-testid="chat-model"]').element as HTMLSelectElement).value).toBe('model')
  })
  it('requires an explicit key, streams once and persists history without a secret', async () => {
    await setup()
    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
    await ready(); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(mocks.stream.mock.calls[0][0]).toBe('KEY_MUST_STAY_PRIVATE')
    expect(mocks.api.start.mock.calls[0][1]).toMatchObject({ api_key_id: 10, model: 'model', content: 'hello' })
    expect(mocks.api.save.mock.calls.at(-1)?.[2]).toMatchObject({ content: 'answer', reasoning: 'thought', status: 'complete' })
    expect(wrapper.text()).toContain('answer')
    expect(JSON.stringify(mocks.api.start.mock.calls) + JSON.stringify(mocks.api.save.mock.calls) + wrapper.html() + JSON.stringify(localStorage)).not.toContain('KEY_MUST_STAY_PRIVATE')
  })
  it('restores historical key/model and messages from the server', async () => {
    const message = { id: 'm', role: 'user', content: 'cross-device history', reasoning: '', model: 'model', status: 'complete', created_at: conversation.updated_at } as ChatMessage
    mocks.api.get.mockResolvedValue({ conversation, messages: [message], attachments: [] })
    await setup('/chat/c'); await flushPromises()
    expect(wrapper.text()).toContain('cross-device history')
    expect((wrapper.get('[data-testid="chat-key"]').element as HTMLSelectElement).value).toBe('10')
    expect((wrapper.get('[data-testid="chat-model"]').element as HTMLSelectElement).value).toBe('model')
  })
  it('stops generation before navigating away and saves an interrupted reply', async () => {
    mocks.stream.mockImplementation((_key: string, _cid: string, _body: string, signal: AbortSignal, delta: (text: string, reasoning: string) => void) => {
      delta('partial', '')
      return new Promise<void>((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
    })
    await setup(); await ready(); await wrapper.get('form').trigger('submit'); await flushPromises()
    await router.push('/other'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(mocks.api.save.mock.calls.at(-1)?.[2]).toMatchObject({ status: 'interrupted', content: 'partial' })
  })
  it('blocks cross-window parallel generation', async () => {
    mocks.api.get.mockResolvedValue({ conversation, messages: [{ id: 'a', role: 'assistant', status: 'generating', content: '', reasoning: '', model: 'model', created_at: conversation.updated_at }], attachments: [] })
    await setup('/chat/c')
    expect(wrapper.text()).toContain('chat.otherWindow')
    expect(wrapper.get('[data-testid="chat-send"]').attributes('disabled')).toBeDefined()
  })
  it('refreshes completed turns from another window before building the model request', async () => {
    await setup('/chat/c'); await ready()
    const updated = { ...conversation, updated_at: '2026-10-21T01:02:03.123456Z' }
    const messages = [
      { id: 'remote-user', role: 'user', content: 'remote question', reasoning: '', model: 'model', status: 'complete', created_at: conversation.updated_at },
      { id: 'remote-reply', role: 'assistant', content: 'remote answer', reasoning: '', model: 'model', status: 'complete', created_at: conversation.updated_at }
    ] as ChatMessage[]
    mocks.api.get.mockResolvedValue({ conversation: updated, messages, attachments: [] })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(JSON.parse(mocks.stream.mock.calls[0][2]).messages.map((m: { content: string }) => m.content)).toEqual(['remote question', 'remote answer', 'hello'])
    expect(mocks.api.start.mock.calls[0][1].history_updated_at).toBe(updated.updated_at)
    expect(mocks.stream).toHaveBeenCalledTimes(1)
  })
  it('keeps the draft and makes no paid request when history changes during request assembly', async () => {
    await setup('/chat/c'); await ready()
    mocks.api.start.mockRejectedValueOnce({ status: 409, code: 'CHAT_HISTORY_CHANGED', message: 'history changed' })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.stream).not.toHaveBeenCalled()
    expect(mocks.api.save).not.toHaveBeenCalled()
    expect((wrapper.get('[data-testid="chat-input"]').element as HTMLTextAreaElement).value).toBe('hello')
    expect(wrapper.text()).toContain('history changed')
  })
  it('never repeats the model call when final history saving fails', async () => {
    mocks.api.save.mockRejectedValue(new Error('offline'))
    await setup(); await ready(); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(wrapper.text()).toContain('chat.errors.save')
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    mocks.api.save.mockResolvedValue(undefined)
    const retry = wrapper.findAll('button').find(button => button.text() === 'chat.retrySave')
    await retry!.trigger('click'); await flushPromises()
    expect(mocks.stream).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).not.toContain('chat.errors.save')
  })
})
