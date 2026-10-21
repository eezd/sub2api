<template>
  <AppLayout>
    <div class="flex h-[calc(100dvh-9rem)] min-h-[520px] flex-col gap-3 lg:flex-row">
      <aside class="flex shrink-0 flex-col rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900 lg:w-64">
        <div class="flex items-center justify-between gap-2">
          <h1 class="font-semibold">{{ t('chat.title') }}</h1>
          <button type="button" class="btn btn-primary px-3 py-1.5 text-sm" :disabled="working" @click="newConversation">{{ t('chat.new') }}</button>
        </div>
        <div class="mt-3 flex max-h-32 gap-2 overflow-auto lg:max-h-none lg:flex-1 lg:flex-col">
          <button v-for="c in conversations" :key="c.id" type="button" class="shrink-0 rounded-lg px-3 py-2 text-left text-sm lg:w-full" :class="detail?.conversation.id === c.id ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/40' : 'hover:bg-gray-50 dark:hover:bg-dark-800'" :disabled="working" @click="router.push('/chat/' + c.id)">
            <span class="block max-w-48 truncate">{{ c.title }}</span>
            <span v-if="c.deleting" class="text-xs text-amber-600">{{ t('chat.deletePending') }}</span>
          </button>
          <button v-if="hasMore" type="button" class="btn btn-ghost text-sm" :disabled="loadingList" @click="moreConversations">{{ t('chat.more') }}</button>
        </div>
        <p v-if="limits" class="mt-3 text-xs text-gray-500">{{ t('chat.storage', { used: (limits.used_bytes / 1e6).toFixed(1) }) }}</p>
      </aside>

      <section class="flex min-h-0 min-w-0 flex-1 flex-col rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
        <div class="flex flex-wrap items-end gap-3 border-b border-gray-100 p-3 dark:border-dark-800">
          <label class="min-w-32 flex-1 text-xs text-gray-500">{{ t('chat.key') }}
            <select v-model="selectedKey" data-testid="chat-key" class="input mt-1 w-full" :disabled="busy || working || initializingSelection">
              <option :value="null">{{ t('chat.selectKey') }}</option>
              <option v-for="k in keys" :key="k.id" :value="k.id">{{ k.name }} · #{{ k.id }}</option>
            </select>
          </label>
          <label class="min-w-32 flex-1 text-xs text-gray-500">{{ t('chat.model') }}
            <select v-model="selectedModel" data-testid="chat-model" class="input mt-1 w-full" :disabled="busy || working || initializingSelection || modelsLoading || !models.length">
              <option value="">{{ modelsLoading ? t('chat.loading') : t('chat.selectModel') }}</option>
              <option v-for="model in models" :key="model" :value="model">{{ model }}</option>
            </select>
          </label>
          <button v-if="selectedKey" type="button" class="btn btn-ghost px-2" :disabled="busy || working || initializingSelection || modelsLoading" :title="t('chat.refreshModels')" @click="loadModels">↻</button>
          <button v-if="detail" type="button" class="btn btn-ghost px-2 text-sm" :disabled="busy || working" @click="renameConversation">{{ t('chat.rename') }}</button>
          <button v-if="detail" type="button" class="btn btn-ghost px-2 text-sm text-red-600" :disabled="busy || working" @click="deleteConversation">{{ t('chat.delete') }}</button>
        </div>
        <div v-if="error" role="alert" class="flex items-center justify-between gap-2 border-b border-red-100 bg-red-50 px-4 py-2 text-sm text-red-700 dark:bg-red-950/20 dark:text-red-400">
          <span>{{ error }}</span>
          <button v-if="unsaved" type="button" class="underline" :disabled="working" @click="retrySave">{{ t('chat.retrySave') }}</button>
          <button v-else type="button" :aria-label="t('chat.dismiss')" @click="error = ''">×</button>
        </div>
        <div ref="scrollArea" class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4" aria-live="polite">
          <p v-if="loadingDetail" class="py-12 text-center text-gray-500">{{ t('chat.loading') }}</p>
          <div v-else-if="!detail?.messages.length" class="mx-auto max-w-xl py-12 text-center text-gray-500">
            <p class="text-lg font-medium text-gray-800 dark:text-gray-100">{{ t('chat.welcome') }}</p>
            <p class="mt-3 text-sm">{{ t('chat.intro') }}</p>
          </div>
          <ChatMessageCard v-for="message in detail?.messages ?? []" :key="message.id" :message="message" :attachments="detail?.attachments.filter(a => a.message_id === message.id) ?? []" :previews="previews" @download="download" />
          <p v-if="remoteGenerating" class="text-sm text-amber-600">{{ t('chat.otherWindow') }}</p>
        </div>
        <form class="border-t border-gray-100 p-3 dark:border-dark-800" @submit.prevent="send">
          <div v-if="pending.length" class="mb-2 flex flex-wrap gap-2">
            <span v-for="a in pending" :key="a.id" class="flex max-w-full items-center gap-2 rounded-lg bg-gray-100 px-2 py-1 text-xs dark:bg-dark-800">
              <span class="truncate">{{ a.filename }}</span>
              <button type="button" :disabled="busy || working" :aria-label="t('chat.removeAttachment')" @click="removeAttachment(a)">×</button>
            </span>
          </div>
          <textarea v-model="draft" data-testid="chat-input" rows="3" class="input w-full resize-none" :disabled="working || busy || !!detail?.conversation.deleting" :placeholder="t('chat.placeholder')" @keydown="onKeydown"></textarea>
          <div class="mt-2 flex items-center justify-between gap-3">
            <div class="min-w-0">
              <label class="btn btn-ghost cursor-pointer px-2 py-1 text-sm" :class="{ 'pointer-events-none opacity-50': busy || working || pending.length >= 5 || detail?.conversation.deleting }">
                {{ working ? t('chat.processing') : t('chat.attach') }}
                <input ref="fileInput" type="file" multiple :accept="CHAT_ACCEPT" class="sr-only" :disabled="busy || working || pending.length >= 5 || !!detail?.conversation.deleting" @change="uploadFiles" />
              </label>
              <span class="hidden text-xs text-gray-400 sm:inline">{{ t('chat.limits') }}</span>
            </div>
            <button v-if="controller" type="button" class="btn btn-secondary" @click="stop">{{ t('chat.stop') }}</button>
            <button v-else type="submit" data-testid="chat-send" class="btn btn-primary" :disabled="!canSend">{{ t('chat.send') }}</button>
          </div>
          <p class="mt-2 text-xs text-gray-400">{{ t('chat.billingNotice') }}</p>
        </form>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ChatMessageCard from '@/components/chat/ChatMessage.vue'
import { chatAPI, chatModels, streamChat, ChatError, type ChatAttachment, type ChatDetail, type ChatLimits, type ChatMessage, type Conversation, type Snapshot } from '@/api/chat'
import * as keysAPI from '@/api/keys'
import type { ApiKey } from '@/types'
import { AttachmentError, buildChatBody, byteLength, CHAT_ACCEPT, extractAttachment } from '@/utils/chatAttachments'
import { SSEError } from '@/utils/sse'
import { useModelSelectionPreference } from '@/composables/useModelSelectionPreference'

const { t } = useI18n(), router = useRouter(), route = useRoute()
const conversations = ref<Conversation[]>([]), detail = ref<ChatDetail | null>(null), limits = ref<ChatLimits | null>(null)
const keys = ref<Pick<ApiKey, 'id' | 'name'>[]>([]), selectedKey = ref<number | null>(null), selectedModel = ref(''), models = ref<string[]>([])
const modelsLoading = ref(false), loadingDetail = ref(false), working = ref(false), loadingList = ref(false), hasMore = ref(false)
const initializingSelection = ref(true)
const error = ref(''), draft = ref(''), previews = ref<Record<string,string>>({}), controller = shallowRef<AbortController | null>(null)
const unsaved = shallowRef<{ cid: string; mid: string; snapshot: Snapshot } | null>(null)
const scrollArea = ref<HTMLElement | null>(null), fileInput = ref<HTMLInputElement | null>(null)
let page = 1, detailVersion = 0, modelVersion = 0, disposed = false
let modelController: AbortController | null = null, sendTask: Promise<void> | null = null, poll: ReturnType<typeof setInterval> | undefined
const pending = computed(() => detail.value?.attachments.filter(a => !a.message_id) ?? [])
const remoteGenerating = computed(() => !controller.value && detail.value?.messages.some(m => m.status === 'generating'))
const busy = computed(() => !!controller.value || !!remoteGenerating.value)
const canSend = computed(() => !initializingSelection.value && !busy.value && !working.value && !loadingDetail.value && !unsaved.value && !detail.value?.conversation.deleting && !!limits.value && !!selectedKey.value && models.value.includes(selectedModel.value) && (!!draft.value.trim() || pending.value.length > 0))

function messageFor(err: unknown): string {
  if (err instanceof AttachmentError) return t(`chat.errors.${err.code}`)
  if (err instanceof ChatError) return err.code === 'http' ? t('chat.errors.http', { status: err.status, message: err.message === 'http' ? '' : err.message }) : t(`chat.errors.${err.code}`)
  if (err instanceof SSEError) return t(err.code === 'aborted' ? 'chat.interrupted' : 'chat.errors.incomplete')
  if (err instanceof Error && err.message === 'CHAT_REQUEST_SIZE') return t('chat.errors.requestSize')
  if (err && typeof err === 'object' && 'message' in err && typeof err.message === 'string') return err.message
  return t('chat.errors.network')
}
function showError(err: unknown) { error.value = messageFor(err) }
const selectionPreference = useModelSelectionPreference('chat', showError)
watch([selectedKey, selectedModel, modelsLoading, loadingDetail], () => {
  if (!initializingSelection.value && !modelsLoading.value && !loadingDetail.value && !disposed && (!selectedKey.value || models.value.length)) selectionPreference.save(selectedKey.value, selectedModel.value)
})
async function refreshList() { conversations.value = await chatAPI.list(); page = 1; hasMore.value = conversations.value.length === 50 }
async function moreConversations() {
  loadingList.value = true
  try { const items = await chatAPI.list(page + 1); conversations.value.push(...items.filter(c => !conversations.value.some(old => old.id === c.id))); page++; hasMore.value = items.length === 50 }
  catch (err) { showError(err) } finally { loadingList.value = false }
}
async function loadKeys() {
  const result: Pick<ApiKey, 'id' | 'name'>[] = []
  for (let n = 1; ; n++) {
    const data = await keysAPI.list(n, 100, { status: 'active' })
    for (const k of data.items) if (k.status === 'active') result.push({ id: k.id, name: k.name })
    if (n >= data.pages) break
  }
  keys.value = result
}
async function loadModels() {
  const version = ++modelVersion, id = selectedKey.value, previous = selectedModel.value
  modelController?.abort(); modelController = new AbortController()
  models.value = []; selectedModel.value = ''; modelsLoading.value = !!id
  if (!id) return
  try {
    const key = await keysAPI.getById(id)
    if (version !== modelVersion || disposed) return
    if (key.status !== 'active') throw new ChatError('key')
    const available = await chatModels(key.key, modelController.signal)
    if (version !== modelVersion || disposed) return
    models.value = available
    if (available.includes(previous)) selectedModel.value = previous
    else if (!previous && available.length === 1) selectedModel.value = available[0]
    if (!available.length) error.value = t('chat.noModels')
  } catch (err) { if (version === modelVersion && !modelController.signal.aborted) showError(err) }
  finally { if (version === modelVersion) modelsLoading.value = false }
}
watch(selectedKey, () => { if (!initializingSelection.value) void loadModels() })
function clearPreviews() { for (const url of Object.values(previews.value)) URL.revokeObjectURL(url); previews.value = {} }
async function loadPreviews(d: ChatDetail, version: number) {
  for (const a of d.attachments) {
    if (!a.mime_type.startsWith('image/')) continue
    try {
      const blob = await chatAPI.download(a.id)
      if (disposed || version !== detailVersion) return
      previews.value[a.id] = URL.createObjectURL(blob)
    } catch (err) { if (version === detailVersion) showError(err) }
  }
}
async function loadConversation(id: string, restoreSelection = true) {
  if (id === detail.value?.conversation.id) return
  const version = ++detailVersion
  clearPreviews(); detail.value = null; draft.value = ''; loadingDetail.value = !!id
  if (!initializingSelection.value) error.value = unsaved.value ? t('chat.errors.save') : ''
  if (!id) return
  try {
    const d = await chatAPI.get(id)
    if (disposed || version !== detailVersion) return
    detail.value = d
    if (restoreSelection) {
      selectedModel.value = d.conversation.model
      if (!initializingSelection.value && selectedKey.value === d.conversation.api_key_id) await loadModels()
      else selectedKey.value = d.conversation.api_key_id
    }
    void loadPreviews(d, version)
    await scrollBottom()
  } catch (err) { if (version === detailVersion) showError(err) }
  finally { if (version === detailVersion) loadingDetail.value = false }
}
watch(() => route.params.id, id => { void loadConversation(typeof id === 'string' ? id : '') })
async function ensureConversation(): Promise<ChatDetail> {
  if (detail.value) return detail.value
  const c = await chatAPI.create()
  detail.value = { conversation: c, messages: [], attachments: [] }; conversations.value.unshift(c)
  await router.replace('/chat/' + c.id)
  return detail.value
}
async function newConversation() { await stop(); await router.push('/chat') }
async function renameConversation() {
  const c = detail.value?.conversation; if (!c) return
  const title = window.prompt(t('chat.renamePrompt'), c.title); if (!title?.trim()) return
  working.value = true
  try { await chatAPI.rename(c.id, title.trim()); c.title = title.trim(); await refreshList() }
  catch (err) { showError(err) } finally { working.value = false }
}
async function deleteConversation() {
  const c = detail.value?.conversation; if (!c || !window.confirm(t('chat.deleteConfirm'))) return
  working.value = true
  try { await chatAPI.remove(c.id); if (unsaved.value?.cid === c.id) unsaved.value = null; working.value = false; await router.push('/chat'); await refreshList(); limits.value = await chatAPI.limits() }
  catch (err) { showError(err) } finally { working.value = false }
}
async function uploadFiles(event: Event) {
  const files = Array.from((event.target as HTMLInputElement).files ?? [])
  if (!files.length || busy.value || working.value) return
  working.value = true; error.value = ''
  try {
    if (pending.value.length + files.length > 5) { error.value = t('chat.errors.attachmentCount'); return }
    const d = await ensureConversation()
    for (const file of files) {
      if (limits.value && limits.value.used_bytes + file.size > limits.value.storage_bytes) { error.value = t('chat.errors.storage'); break }
      const text = await extractAttachment(file)
      if (limits.value && limits.value.used_bytes + file.size + byteLength(text) > limits.value.storage_bytes) { error.value = t('chat.errors.storage'); break }
      const a = await chatAPI.upload(d.conversation.id, file, text)
      d.attachments.push(a)
      if (a.mime_type.startsWith('image/')) previews.value[a.id] = URL.createObjectURL(file)
      limits.value = await chatAPI.limits()
    }
  } catch (err) { showError(err) }
  finally { working.value = false; if (fileInput.value) fileInput.value.value = '' }
}
async function removeAttachment(a: ChatAttachment) {
  working.value = true
  try {
    await chatAPI.removeAttachment(a.id)
    if (detail.value) detail.value.attachments = detail.value.attachments.filter(item => item.id !== a.id)
    if (previews.value[a.id]) { URL.revokeObjectURL(previews.value[a.id]); delete previews.value[a.id] }
    limits.value = await chatAPI.limits()
  } catch (err) { showError(err) } finally { working.value = false }
}
async function download(a: ChatAttachment) {
  try {
    const url = URL.createObjectURL(await chatAPI.download(a.id)), link = document.createElement('a')
    link.href = url; link.download = a.filename; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) { showError(err) }
}
async function scrollBottom() { await nextTick(); const el = scrollArea.value; if (el) el.scrollTop = el.scrollHeight }
function onKeydown(event: KeyboardEvent) { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); send() } }
function send() { if (!canSend.value) return; sendTask = runSend(); void sendTask }
async function runSend() {
  working.value = true; error.value = ''
  const abort = new AbortController(); controller.value = abort
  let reply: ChatMessage | undefined, cid = '', revision = 0, timer: ReturnType<typeof setInterval> | undefined, saving: Promise<void> | null = null, saveError: unknown
  const snapshot = (status: Snapshot['status']): Snapshot => ({ content: reply!.content, reasoning: reply!.reasoning, status, revision: ++revision, error: reply!.error })
  const pulse = () => {
    if (saving || !reply) return
    saving = chatAPI.save(cid, reply.id, snapshot('generating')).catch(err => { saveError = err; abort.abort() }).finally(() => { saving = null })
  }
  try {
    const keyID = selectedKey.value!, model = selectedModel.value
    const key = await keysAPI.getById(keyID)
    if (key.status !== 'active') throw new ChatError('key')
    cid = (await ensureConversation()).conversation.id
    detail.value = await chatAPI.get(cid)
    const d = detail.value
    const files = [...pending.value], text = draft.value
    const body = await buildChatBody(model, d.messages, d.attachments, text, files, chatAPI.download, limits.value!.request_bytes)
    if (abort.signal.aborted) return
    const turn = await chatAPI.start(cid, { request_id: crypto.randomUUID(), api_key_id: keyID, model, content: text, attachment_ids: files.map(a => a.id), history_updated_at: d.conversation.updated_at })
    if (turn.existing) { detail.value = await chatAPI.get(cid); error.value = t('chat.existingTurn'); return }
    const now = new Date().toISOString(), base = { model, api_key_id: keyID, reasoning: '', revision: 0, error: '', created_at: now, updated_at: now }
    d.messages.push({ ...base, id: turn.user_message_id, role: 'user', content: text, status: 'complete' }, { ...base, id: turn.assistant_message_id, role: 'assistant', content: '', status: 'generating' })
    for (const a of files) a.message_id = turn.user_message_id
    reply = d.messages[d.messages.length - 1]
    draft.value = ''; working.value = false
    timer = setInterval(pulse, 2000)
    await scrollBottom()
    if (abort.signal.aborted) throw new SSEError('aborted')
    let size = 0
    await streamChat(key.key, cid, body, abort.signal, (content, reasoning) => {
      if (content.includes('\0') || reasoning.includes('\0')) throw new ChatError('stream')
      size += byteLength(content) + byteLength(reasoning)
      if (size > 8 * 1024 * 1024) throw new ChatError('replySize')
      const el = scrollArea.value, nearBottom = !el || el.scrollHeight - el.scrollTop - el.clientHeight < 120
      reply!.content += content; reply!.reasoning += reasoning
      if (nearBottom) void scrollBottom()
    })
    reply.status = 'complete'
  } catch (err) {
    if (reply) { reply.status = abort.signal.aborted ? 'interrupted' : 'failed'; reply.error = Array.from(saveError ? t('chat.errors.save') : abort.signal.aborted ? t('chat.interrupted') : messageFor(err)).slice(0, 250).join(''); error.value = reply.error }
    else { if (!abort.signal.aborted) showError(err); if (cid) { try { detail.value = await chatAPI.get(cid) } catch (loadError) { showError(loadError) } } }
  } finally {
    clearInterval(timer); await saving
    if (reply) {
      if (reply.status === 'generating') { reply.status = 'interrupted'; reply.error = t('chat.interrupted') }
      const final = snapshot(reply.status)
      try { await chatAPI.save(cid, reply.id, final) }
      catch { unsaved.value = { cid, mid: reply.id, snapshot: final }; error.value = t('chat.errors.save') }
    }
    controller.value = null; working.value = false; sendTask = null
    try { await refreshList() } catch (err) { showError(err) }
  }
}
async function stop() { controller.value?.abort(); await sendTask }
async function retrySave() {
  const data = unsaved.value; if (!data) return
  working.value = true
  try { await chatAPI.save(data.cid, data.mid, data.snapshot); unsaved.value = null; error.value = '' }
  catch (err) { showError(err) } finally { working.value = false }
}
onBeforeRouteLeave(async () => { if (working.value && !controller.value) return false; await stop() })
onBeforeRouteUpdate(async to => { if (to.params.id !== detail.value?.conversation.id) { if (working.value && !controller.value) return false; await stop() } })
function beforeUnload() { controller.value?.abort() }
onMounted(async () => {
  window.addEventListener('beforeunload', beforeUnload)
  try {
    await Promise.all([loadKeys(), refreshList(), chatAPI.limits().then(value => { limits.value = value })])
    if (disposed) return
    const saved = selectionPreference.load()
    const restoreSaved = !!saved && (saved.keyID === null || keys.value.some(k => k.id === saved.keyID))
    if (saved && restoreSaved) { selectedKey.value = saved.keyID; selectedModel.value = saved.model }
    await loadConversation(typeof route.params.id === 'string' ? route.params.id : '', !restoreSaved)
    await nextTick()
    await loadModels()
  }
  catch (err) { showError(err) }
  initializingSelection.value = false
  if (disposed) return
  poll = setInterval(async () => {
    if (!remoteGenerating.value || !detail.value || working.value) return
    const id = detail.value.conversation.id
    try { const d = await chatAPI.get(id); if (detail.value?.conversation.id === id && !controller.value) detail.value = d }
    catch (err) { showError(err) }
  }, 5000)
})
onBeforeUnmount(() => { disposed = true; ++detailVersion; ++modelVersion; controller.value?.abort(); modelController?.abort(); clearInterval(poll); clearPreviews(); window.removeEventListener('beforeunload', beforeUnload) })
</script>
