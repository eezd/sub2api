import type { Account, ClaudeModel } from '@/types'

export interface ModelTraceCandidate {
  model: string
  display_name: string
  family_name: string
  probability: number
}

export interface ModelTraceResult {
  requested_model: string
  tested_model: string
  expected_model_in_bank: boolean
  matches_expected: boolean | null
  prediction: string
  prediction_name: string
  probability: number
  family_prediction_name: string
  family_probability: number
  used_outputs: number
  results: ModelTraceCandidate[]
  disclaimer: string
}

export interface ModelTraceProgress {
  attempt: number
  max_attempts: number
  received: number
  target: number
  accepted: boolean
  parsed_numbers: number
  minimum_numbers: number
  error?: string
}

export const supportsDegradationCheckAccount = (account: Pick<Account, 'platform' | 'type'> | null): boolean => {
  if (!account || !['openai', 'anthropic'].includes(account.platform)) return false
  return ['oauth', 'setup-token', 'apikey'].includes(account.type)
}

export const filterDegradationCheckModels = (models: ClaudeModel[]): ClaudeModel[] => models.filter((model) => {
  const id = model.id.toLowerCase()
  return !(
    id.startsWith('gpt-image-') ||
    id.startsWith('dall-e') ||
    id.startsWith('sora') ||
    id.includes('whisper') ||
    id.includes('transcribe') ||
    id.includes('realtime') ||
    id.includes('audio') ||
    id.endsWith('-tts')
  )
})

export const formatModelTracePercent = (value: number) => `${(value * 100).toFixed(1)}%`

const PREVIEW_CSP = "default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline';"

const stripUnsafeMarkup = (markup: string) => markup
  .replace(/<script\b[^>]*>[\s\S]*?<\/script\s*>/gi, '')
  .replace(/<script\b[^>]*\/\s*>/gi, '')
  .replace(/<base\b[^>]*>/gi, '')
  .replace(/<meta\b(?=[^>]*http-equiv\s*=\s*["']?refresh\b)[^>]*>/gi, '')
  .replace(/<meta\b(?=[^>]*http-equiv\s*=\s*["']?content-security-policy\b)[^>]*>/gi, '')

// The policy must be the first element the parser sees: it then lands in the
// implicit <head> before any model-controlled markup can request a resource.
// Model-supplied doctype/html/head tags after it become ignorable parse errors.
const addPreviewPolicy = (markup: string) =>
  `<!doctype html><meta http-equiv="Content-Security-Policy" content="${PREVIEW_CSP}">${markup}`

export const buildPreviewDocument = (raw: string): string => {
  const fencedBlocks = [...raw.matchAll(/```(?:html|svg|xml)?\s*([\s\S]*?)```/gi)].map((match) => match[1])
  const candidates = [...fencedBlocks, raw]

  for (const candidate of candidates) {
    const starts = [candidate.search(/<!doctype\s+html/i), candidate.search(/<html(?:\s|>)/i), candidate.search(/<svg(?:\s|>)/i)]
      .filter((index) => index >= 0)
    if (starts.length === 0) continue

    const markup = candidate.slice(Math.min(...starts)).trim()
    if (!/<svg(?:\s|>)/i.test(markup)) continue
    return addPreviewPolicy(stripUnsafeMarkup(markup))
  }

  return ''
}
