import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ChatError, chatModels, streamChat } from '../chat'
import { buildChatBody, byteLength, extractAttachment } from '@/utils/chatAttachments'
import type { ChatAttachment, ChatMessage } from '../chat'

const fetchMock = vi.fn()
beforeEach(() => { vi.stubGlobal('fetch', fetchMock); fetchMock.mockReset(); localStorage.clear() })
const event = (value: unknown) => `data: ${JSON.stringify(value)}\r\n\r\n`
function sse(text: string) {
  const bytes = new TextEncoder().encode(text)
  return new Response(new ReadableStream({ start(controller) { for (let n = 0; n < bytes.length; n++) controller.enqueue(bytes.slice(n, n + 1)); controller.close() } }), { headers: { 'Content-Type': 'text/event-stream' } })
}
const delta = (content: string, reasoning_content?: string) => event({ choices: [{ index: 0, delta: { content, reasoning_content, tool_calls: [] }, finish_reason: null }] })
const finish = event({ choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] }) + 'data: [DONE]\r\n\r\n'

describe('Chat gateway contracts', () => {
  it('handles split UTF-8, reasoning, usage and DONE; sends only the selected key', async () => {
    localStorage.setItem('auth_token', 'PRIVATE_PANEL_TOKEN')
    fetchMock.mockResolvedValue(sse(': heartbeat\r\n\r\n' + delta('你好', '思考') + event({ choices: [], usage: { total_tokens: 3 } }) + finish))
    const chunks: string[] = []
    await streamChat('SELECTED_KEY', 'conversation', '{}', new AbortController().signal, (content, reasoning) => chunks.push(content + reasoning))
    expect(chunks.join('')).toBe('你好思考')
    const options = fetchMock.mock.calls[0][1]
    expect(options.headers.Authorization).toBe('Bearer SELECTED_KEY')
    expect(options.headers.session_id).toBe('conversation')
    expect(options.credentials).toBe('omit'); expect(options.redirect).toBe('error')
    expect(JSON.stringify(options)).not.toContain('PRIVATE_PANEL_TOKEN')
  })
  it.each([
    delta('partial'),
    delta('partial') + 'data: [DONE]\n\n',
    delta('partial') + event({ choices: [{ finish_reason: 'length', delta: {} }] }) + 'data: [DONE]\n\n',
    'data: {broken}\n\n',
    event({ error: { message: 'failed' } })
  ])('does not call a partial or failed stream complete and never retries', async body => {
    fetchMock.mockResolvedValue(sse(body))
    await expect(streamChat('KEY', 'c', '{}', new AbortController().signal, () => {})).rejects.toBeInstanceOf(Error)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('stops and cancels a pending stream on abort', async () => {
    const cancel = vi.fn(), abort = new AbortController()
    fetchMock.mockResolvedValue(new Response(new ReadableStream({ cancel }), { headers: { 'Content-Type': 'text/event-stream' } }))
    const running = streamChat('KEY', 'c', '{}', abort.signal, () => {})
    await Promise.resolve(); abort.abort()
    await expect(running).rejects.toBeInstanceOf(Error)
    expect(cancel).toHaveBeenCalled()
  })
  it('reports gateway errors without retrying paid requests', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ error: { message: 'quota exhausted for KEY' } }), { status: 429, headers: { 'Content-Type': 'application/json' } }))
    await expect(streamChat('KEY', 'c', '{}', new AbortController().signal, () => {})).rejects.toMatchObject({ code: 'http', status: 429, message: 'quota exhausted for [REDACTED]' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
  it('loads key-scoped models and excludes known non-chat models', async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ data: [{ id: 'gpt-chat' }, { id: 'claude-chat' }, { id: 'gpt-image-1' }, { id: 'text-embedding-3' }, { id: 'gpt-chat' }] })))
    expect(await chatModels('KEY', new AbortController().signal)).toEqual(['claude-chat', 'gpt-chat'])
    fetchMock.mockResolvedValue(new Response('{"wrong":[]}'))
    await expect(chatModels('KEY', new AbortController().signal)).rejects.toBeInstanceOf(ChatError)
  })
})

describe('Chat request assembly', () => {
  const file = (fields: Partial<ChatAttachment>): ChatAttachment => ({ id: 'a', conversation_id: 'c', message_id: null, filename: 'notes.txt', mime_type: 'text/plain', size: 3, extracted_text: '内容', ...fields })
  const history = [{ id: 'user', role: 'user', content: 'previous', reasoning: '' }, { id: 'reply', role: 'assistant', content: 'answer', reasoning: 'thought' }] as ChatMessage[]
  it('keeps history and reasoning, inserts document text and uses inline images', async () => {
    const a = file({}), image = file({ id: 'image', mime_type: 'image/png', extracted_text: '' })
    const body = await buildChatBody('model', history, [a, image], 'next', [a, image], async () => new Blob(['png'], { type: 'image/png' }), 1_000_000)
    const parsed = JSON.parse(body)
    expect(parsed.messages.map((m: { role: string }) => m.role)).toEqual(['user', 'assistant', 'user'])
    expect(parsed.messages[1].reasoning_content).toBe('thought')
    expect(parsed.messages[2].content[1].text).toContain('内容')
    expect(parsed.messages[2].content[2].image_url.url).toBe('data:image/png;base64,cG5n')
    expect(body).not.toContain('/chat/attachments/')
  })
  it('measures serialized UTF-8, JSON escaping and Base64; never truncates', async () => {
    expect(byteLength('中文')).toBe(6)
    const content = '\u0001'.repeat(100)
    await expect(buildChatBody('model', history, [], content, [], vi.fn(), 300)).rejects.toThrow('CHAT_REQUEST_SIZE')
    const download = vi.fn()
    await expect(buildChatBody('model', [], [file({ mime_type: 'image/png', size: 300 })], '', [], download, 200)).rejects.toThrow('CHAT_REQUEST_SIZE')
    expect(download).not.toHaveBeenCalled()
  })
  it('validates raw file sizes and supported extensions before upload', async () => {
    await expect(extractAttachment(new File([], 'empty.txt'))).rejects.toMatchObject({ code: 'fileSize' })
    await expect(extractAttachment(new File(['test'], 'a.exe'))).rejects.toMatchObject({ code: 'fileType' })
    const image = new File(['png'], 'a.png')
    expect(await extractAttachment(image)).toBe('')
  })
})
