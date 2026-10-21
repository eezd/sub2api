import { apiClient } from './client'
import { buildGatewayUrl } from './url'
import { consumeSSE } from '@/utils/sse'

export interface Conversation { id: string; title: string; api_key_id: number | null; model: string; deleting: boolean; updated_at: string }
export interface ChatAttachment { id: string; conversation_id: string; message_id: string | null; filename: string; mime_type: string; size: number; extracted_text: string }
export type ReplyStatus = 'generating' | 'complete' | 'interrupted' | 'failed'
export interface ChatMessage { id: string; role: 'user' | 'assistant'; content: string; reasoning: string; model: string; api_key_id: number | null; status: ReplyStatus; revision: number; error: string; created_at: string; updated_at: string }
export interface ChatDetail { conversation: Conversation; messages: ChatMessage[]; attachments: ChatAttachment[] }
export interface ChatLimits { used_bytes: number; storage_bytes: number; file_bytes: number; text_bytes: number; request_bytes: number }
export interface Snapshot { content: string; reasoning: string; status: ReplyStatus; revision: number; error: string }
export interface TurnInput { request_id: string; api_key_id: number; model: string; content: string; attachment_ids: string[]; history_updated_at: string }
export interface Turn { user_message_id: string; assistant_message_id: string; existing: boolean }
const conversations = '/chat/conversations'

export const chatAPI = {
  async list(page = 1) { return (await apiClient.get<Conversation[]>(conversations, { params: { page } })).data },
  async create() { return (await apiClient.post<Conversation>(conversations)).data },
  async get(id: string) { return (await apiClient.get<ChatDetail>(`${conversations}/${id}`)).data },
  async rename(id: string, title: string) { await apiClient.patch(`${conversations}/${id}`, { title }) },
  async remove(id: string) { await apiClient.delete(`${conversations}/${id}`) },
  async limits() { return (await apiClient.get<ChatLimits>('/chat/limits')).data },
  async upload(id: string, file: File, text: string) {
    const data = new FormData(); data.append('file', file); data.append('extracted_text', text)
    return (await apiClient.post<ChatAttachment>(`${conversations}/${id}/attachments`, data, { headers: { 'Content-Type': undefined }, timeout: 120_000 })).data
  },
  async download(id: string) { return (await apiClient.get<Blob>(`/chat/attachments/${id}`, { responseType: 'blob' })).data },
  async removeAttachment(id: string) { await apiClient.delete(`/chat/attachments/${id}`) },
  async start(id: string, input: TurnInput) { return (await apiClient.post<Turn>(`${conversations}/${id}/turns`, input)).data },
  async save(id: string, mid: string, input: Snapshot) { await apiClient.put(`${conversations}/${id}/messages/${mid}`, input) }
}

export class ChatError extends Error {
  constructor(public code: 'http' | 'stream' | 'incomplete' | 'key' | 'requestSize' | 'replySize', public status?: number, message?: string) { super(message ?? code) }
}
const record = (v: unknown): Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : {}
async function gatewayError(response: Response, key: string): Promise<never> {
  const contentType = response.headers.get('content-type') ?? ''
  let message: string | undefined
  if (contentType.includes('application/json')) {
    const body: unknown = await response.json()
    const error = record(record(body).error)
    if (typeof error.message === 'string') message = error.message.split(key).join('[REDACTED]').slice(0, 500)
  } else await response.body?.cancel()
  throw new ChatError('http', response.status, message)
}
export async function chatModels(key: string, signal: AbortSignal): Promise<string[]> {
  const response = await fetch(buildGatewayUrl('/v1/models'), { headers: { Authorization: `Bearer ${key}` }, signal, credentials: 'omit', cache: 'no-store', redirect: 'error' })
  if (!response.ok) return gatewayError(response, key)
  const body: unknown = await response.json()
  if (!Array.isArray(record(body).data)) throw new ChatError('stream')
  const ids: string[] = []
  for (const item of record(body).data as unknown[]) {
    const id = record(item).id
    if (typeof id !== 'string' || !id) throw new ChatError('stream')
    // /models 没有能力声明，仅排除明确的非聊天模型，其余交由网关验证。
    if (!/(?:embedding|whisper|tts-|dall-e|gpt-image|sora|imagegen|veo-)/i.test(id)) ids.push(id)
  }
  return [...new Set(ids)].sort()
}

// 一次发送只产生一次模型请求；流异常与保存失败均不能重发付费请求。
export async function streamChat(key: string, conversationID: string, body: string, signal: AbortSignal, onDelta: (content: string, reasoning: string) => void): Promise<void> {
  const response = await fetch(buildGatewayUrl('/v1/chat/completions'), {
    method: 'POST', headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json', Accept: 'text/event-stream', session_id: conversationID },
    body, signal, credentials: 'omit', cache: 'no-store', redirect: 'error'
  })
  if (!response.ok) return gatewayError(response, key)
  let finish = ''
  await consumeSSE(response, value => {
    if (value === '[DONE]') { if (finish !== 'stop') throw new ChatError('incomplete'); return true }
    const event = record(value)
    if (event.error) throw new ChatError('stream')
    if (!Array.isArray(event.choices)) throw new ChatError('stream')
    for (const value of event.choices) {
      const choice = record(value)
      if (choice.index !== undefined && choice.index !== 0) continue
      const delta = record(choice.delta)
      if (typeof choice.finish_reason === 'string') finish = choice.finish_reason
      if (delta.tool_calls != null) {
        if (!Array.isArray(delta.tool_calls)) throw new ChatError('stream')
        if (delta.tool_calls.length) throw new ChatError('incomplete')
      }
      const content = delta.content, reasoning = delta.reasoning_content
      if (content !== undefined && content !== null && typeof content !== 'string') throw new ChatError('stream')
      if (reasoning !== undefined && reasoning !== null && typeof reasoning !== 'string') throw new ChatError('stream')
      onDelta(typeof content === 'string' ? content : '', typeof reasoning === 'string' ? reasoning : '')
    }
    return false
  }, signal)
}
