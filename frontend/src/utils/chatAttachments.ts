import type { ChatAttachment, ChatMessage } from '@/api/chat'

export const FILE_LIMIT = 20 * 1024 * 1024
export const TEXT_LIMIT = 2_000_000
export const REQUEST_LIMIT = 95_000_000
export const CHAT_ACCEPT = '.png,.jpg,.jpeg,.webp,.pdf,.txt,.md,.markdown,.csv,.json,.jsonl,.yaml,.yml,.toml,.xml,.html,.css,.js,.ts,.jsx,.tsx,.vue,.py,.go,.rs,.java,.c,.h,.cpp,.hpp,.sh,.sql,.log,.ini,.conf,.rb,.php,.swift,.kt'
export class AttachmentError extends Error {
  constructor(public code: 'fileSize' | 'fileType' | 'textSize' | 'encoding' | 'pdfPages' | 'pdfEmpty' | 'pdfInvalid') { super(code) }
}
export const byteLength = (text: string) => new TextEncoder().encode(text).byteLength
export async function extractAttachment(file: File): Promise<string> {
  if (file.size === 0 || file.size > FILE_LIMIT) throw new AttachmentError('fileSize')
  const ext = '.' + file.name.split('.').pop()?.toLowerCase()
  if (!CHAT_ACCEPT.split(',').includes(ext)) throw new AttachmentError('fileType')
  if (['.png', '.jpg', '.jpeg', '.webp'].includes(ext)) return ''
  if (ext === '.pdf') return (await import('./chatPDF')).extractPDF(file)
  if (file.size > TEXT_LIMIT) throw new AttachmentError('textSize')
  let text: string
  try { text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer()) } catch { throw new AttachmentError('encoding') }
  if (text.includes('\0')) throw new AttachmentError('encoding')
  return text
}
export async function imageDataURL(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => { if (typeof reader.result === 'string') resolve(reader.result); else reject(new Error('无法读取图片')) }
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(blob)
  })
}
type ContentPart = { type: 'text'; text: string } | { type: 'image_url'; image_url: { url: string } }
type GatewayMessage = { role: 'user' | 'assistant'; content: string | ContentPart[]; reasoning_content?: string }

export async function buildChatBody(model: string, history: ChatMessage[], attachments: ChatAttachment[], draft: string, pending: ChatAttachment[], download: (id: string) => Promise<Blob>, limit: number): Promise<string> {
  const messages: GatewayMessage[] = []
  let estimated = byteLength(draft)
  // 先估算原图的 Base64 下限，避免明知超限还把全部图片读入内存。
  for (const a of attachments) estimated += a.mime_type.startsWith('image/') ? Math.ceil(a.size / 3) * 4 : byteLength(a.extracted_text)
  for (const m of history) estimated += byteLength(m.content) + byteLength(m.reasoning)
  if (estimated > limit) throw new Error('CHAT_REQUEST_SIZE')
  const userContent = async (text: string, files: ChatAttachment[]): Promise<string | ContentPart[]> => {
    const parts: ContentPart[] = []
    if (text) parts.push({ type: 'text', text })
    for (const a of files) {
      if (a.mime_type.startsWith('image/')) parts.push({ type: 'image_url', image_url: { url: await imageDataURL(await download(a.id)) } })
      else parts.push({ type: 'text', text: `\n--- ${a.filename} ---\n${a.extracted_text}\n---\n` })
    }
    return files.some(a => a.mime_type.startsWith('image/')) ? parts : parts.map(p => p.type === 'text' ? p.text : '').join('\n')
  }
  for (const m of history) {
    if (m.role === 'user') messages.push({ role: 'user', content: await userContent(m.content, attachments.filter(a => a.message_id === m.id)) })
    else if (m.content || m.reasoning) messages.push({ role: 'assistant', content: m.content, ...(m.reasoning ? { reasoning_content: m.reasoning } : {}) })
  }
  messages.push({ role: 'user', content: await userContent(draft, pending) })
  const body = JSON.stringify({ model, messages, stream: true, stream_options: { include_usage: true } })
  if (byteLength(body) > Math.min(REQUEST_LIMIT, limit)) throw new Error('CHAT_REQUEST_SIZE')
  return body
}
