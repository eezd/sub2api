<template>
  <AppLayout>
    <div class="flex min-w-0 flex-col gap-4">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-xl font-semibold">{{ t('imageWorkbench.title') }}</h1><p class="mt-1 text-sm text-gray-500">{{ t('imageWorkbench.intro') }}</p></div>
        <button type="button" class="btn btn-secondary" @click="openHistory">{{ t('imageWorkbench.history') }}</button>
      </header>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <div class="grid min-w-0 gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
        <form class="min-w-0 space-y-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="generate">
          <label class="block text-sm font-medium">{{ t('imageWorkbench.key') }}
            <select v-model="keyID" class="input mt-1 w-full" :disabled="working || loading"><option :value="null">{{ t('imageWorkbench.selectKey') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }}</option></select>
          </label>
          <label class="block text-sm font-medium">{{ t('imageWorkbench.protocol') }}
            <select :value="protocol" class="input mt-1 w-full" :disabled="!keyID || modelsLoading || working" @change="changeProtocol"><option value="images">Images</option><option v-if="nativeGemini" value="gemini">Gemini</option></select>
          </label>
          <label class="block text-sm font-medium">{{ t('imageWorkbench.model') }}
            <select v-model="model" class="input mt-1 w-full" :disabled="modelsLoading || working"><option value="">{{ modelsLoading ? t('imageWorkbench.loading') : t('imageWorkbench.selectModel') }}</option><option v-for="id in models" :key="id" :value="id">{{ id }}</option></select>
          </label>
          <p class="text-xs text-gray-500">{{ t('imageWorkbench.modelHint') }}</p>
          <button type="button" class="btn btn-ghost text-xs" :disabled="!keyID || modelsLoading || working" @click="loadModels(protocol)">{{ t('imageWorkbench.refreshModels') }}</button>
          <label class="block text-sm font-medium">{{ t('imageWorkbench.prompt') }}<textarea v-model="prompt" data-testid="image-prompt" class="input mt-1 w-full resize-y" rows="5" maxlength="8000" :placeholder="t('imageWorkbench.promptPlaceholder')" :disabled="working" /></label>
          <div v-if="protocol === 'images'" class="grid grid-cols-2 gap-3">
            <label class="text-sm">{{ t('imageWorkbench.size') }}<select v-model="size" class="input mt-1 w-full"><option value="auto">{{ t('imageWorkbench.auto') }}</option><option value="1024x1024">1024 × 1024</option><option value="1536x1024">1536 × 1024</option><option value="1024x1536">1024 × 1536</option></select></label>
            <label class="text-sm">{{ t('imageWorkbench.quality') }}<select v-model="quality" class="input mt-1 w-full"><option v-for="q in ['auto', 'low', 'medium', 'high']" :key="q" :value="q">{{ t(`imageWorkbench.${q}`) }}</option></select></label>
          </div>
          <div v-else class="grid grid-cols-2 gap-3">
            <label class="text-sm">{{ t('imageWorkbench.ratio') }}<select v-model="ratio" class="input mt-1 w-full"><option value="auto">{{ t('imageWorkbench.auto') }}</option><option v-for="r in ratios" :key="r" :value="r">{{ r }}</option></select></label>
            <label class="text-sm">{{ t('imageWorkbench.resolution') }}<select v-model="resolution" class="input mt-1 w-full"><option value="auto">{{ t('imageWorkbench.auto') }}</option><option v-for="r in ['1K', '2K', '4K']" :key="r" :value="r">{{ r }}</option></select></label>
          </div>
          <label class="block text-sm">{{ t('imageWorkbench.count') }}<select v-model.number="count" class="input mt-1 w-full"><option v-for="n in 4" :key="n" :value="n">{{ n }}</option></select></label>
          <section class="rounded-lg border border-dashed border-gray-300 p-3 dark:border-dark-600" @dragover.prevent @drop.prevent="dropFiles" @paste="pasteFiles">
            <div class="flex items-center justify-between gap-2"><span class="text-sm font-medium">{{ t('imageWorkbench.references') }} · {{ references.length }}/5</span><label class="btn btn-ghost cursor-pointer px-2 text-sm" :class="{ 'pointer-events-none opacity-50': working || references.length >= 5 }">{{ t('imageWorkbench.upload') }}<input ref="fileInput" class="sr-only" type="file" accept="image/png,image/jpeg,image/webp" multiple :disabled="working || references.length >= 5" @change="selectFiles" /></label></div>
            <p class="mt-1 text-xs text-gray-500">{{ t('imageWorkbench.referenceHint') }}</p>
            <div class="mt-2 grid grid-cols-3 gap-2"><div v-for="a in references" :key="a.id" class="relative min-w-0"><img v-if="previews[a.id]" :src="previews[a.id]" :alt="a.filename" class="h-20 w-full rounded-lg object-cover" /><span v-else class="block h-20 truncate text-xs">{{ a.filename }}</span><button type="button" class="absolute right-0 top-0 rounded bg-black/60 px-1.5 text-white" :aria-label="t('imageWorkbench.removeReference')" :disabled="working" @click="removeReference(a)">×</button></div></div>
          </section>
          <section v-if="unusedDrafts.length" class="space-y-2 text-xs">
            <p class="font-medium">{{ t('imageWorkbench.unusedDrafts') }}</p>
            <div v-for="a in unusedDrafts" :key="a.id" class="flex min-w-0 items-center gap-2">
              <span class="min-w-0 flex-1 truncate">{{ a.filename }} · {{ (a.size / 1e6).toFixed(1) }} MB</span>
              <button type="button" class="text-primary-600" :disabled="working || references.length >= 5" @click="addDraftReference(a)">{{ t('imageWorkbench.add') }}</button>
              <button type="button" class="text-red-600" :disabled="working" @click="removeReference(a)">{{ t('imageWorkbench.deleteDraft') }}</button>
            </div>
          </section>
          <div v-if="limits" class="text-xs text-gray-500"><p>{{ t('imageWorkbench.storage', { used: (limits.used_bytes / 1e6).toFixed(1), reserved: (limits.reserved_bytes / 1e6).toFixed(1) }) }}</p><p class="mt-1">{{ t('imageWorkbench.reserveHint', { size: (count * fileLimit / 1e6).toFixed(1) }) }}</p></div>
          <button type="submit" data-testid="image-generate" class="btn btn-primary w-full" :disabled="!canGenerate">{{ working ? t('imageWorkbench.processing') : running ? t('imageWorkbench.running') : t('imageWorkbench.generate') }}</button>
          <button v-if="uncertainInput" type="button" class="btn btn-secondary w-full" :disabled="working" @click="resubmit">{{ t('imageWorkbench.checkSubmission') }}</button>
          <p class="text-xs text-gray-500">{{ t('imageWorkbench.billingNotice') }}</p>
        </form>
        <section class="min-w-0 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900">
          <div class="flex flex-wrap items-center justify-between gap-2"><h2 class="font-semibold">{{ t('imageWorkbench.results') }}</h2><div v-if="job" class="flex flex-wrap items-center gap-2"><span class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-dark-800">{{ t(`imageWorkbench.status.${job.status}`) }}</span><button type="button" class="btn btn-ghost text-sm" :disabled="working || !!uncertainInput" @click="reuseJob(job)">{{ t('imageWorkbench.reuse') }}</button><button v-if="job.status === 'running'" type="button" class="btn btn-secondary text-sm" :disabled="working" @click="cancelJob">{{ t('imageWorkbench.cancel') }}</button></div></div>
          <div v-if="!job" class="flex min-h-80 flex-col items-center justify-center px-4 text-center text-gray-500"><Icon name="lightbulb" size="xl" /><p class="mt-4">{{ t('imageWorkbench.empty') }}</p></div>
          <template v-else>
            <p class="mt-3 whitespace-pre-wrap break-words text-sm text-gray-600 dark:text-gray-300">{{ job.input.prompt }}</p>
            <p class="mt-1 break-all text-xs text-gray-400">{{ job.input.model }} · {{ new Date(job.created_at).toLocaleString() }}</p>
            <div class="mt-4 grid gap-4 sm:grid-cols-2">
              <article v-for="item in job.items" :key="item.position" class="min-w-0 overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700">
                <button v-if="item.asset_id && previews[item.asset_id]" type="button" class="block w-full bg-gray-50 dark:bg-dark-800" @click="previewID = item.asset_id"><img :src="previews[item.asset_id]" :alt="t('imageWorkbench.resultAlt', { n: item.position + 1 })" class="max-h-96 w-full object-contain" /></button>
                <div v-else class="flex min-h-48 items-center justify-center bg-gray-50 px-4 py-8 text-center text-sm dark:bg-dark-800"><span v-if="item.status === 'pending'" class="animate-pulse text-gray-500">{{ t('imageWorkbench.waiting') }}</span><span v-else class="break-words text-red-600 dark:text-red-300">{{ item.error || t(`imageWorkbench.status.${item.status}`) }}</span></div>
                <div class="flex flex-wrap items-center justify-between gap-2 p-3 text-xs"><span>{{ (item.duration_ms / 1000).toFixed(1) }}s</span><div v-if="item.asset_id" class="flex gap-2"><button type="button" class="btn btn-ghost px-2 py-1 text-xs" @click="download(item.asset_id)">{{ t('imageWorkbench.download') }}</button><button type="button" class="btn btn-ghost px-2 py-1 text-xs" :disabled="working || references.length >= 5" @click="addResultReference(item.asset_id)">{{ t('imageWorkbench.useReference') }}</button></div></div>
              </article>
            </div>
          </template>
        </section>
      </div>
    </div>
    <div v-if="historyOpen" class="fixed inset-0 z-50 flex justify-end bg-black/40" @click.self="historyOpen = false"><section class="flex h-full w-full max-w-md flex-col bg-white p-4 shadow-xl dark:bg-dark-900" role="dialog" aria-modal="true" :aria-label="t('imageWorkbench.history')"><header class="flex items-center justify-between"><h2 class="font-semibold">{{ t('imageWorkbench.history') }}</h2><button type="button" class="btn btn-ghost" :aria-label="t('imageWorkbench.close')" @click="historyOpen = false">×</button></header><div class="mt-3 flex-1 space-y-2 overflow-y-auto"><div v-for="j in history" :key="j.id" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"><button type="button" class="w-full text-left" @click="selectJob(j.id)"><p class="truncate text-sm">{{ j.input.prompt }}</p><p class="mt-1 text-xs text-gray-500">{{ j.input.model }} · {{ t(`imageWorkbench.status.${j.status}`) }}</p><p class="mt-1 text-xs text-gray-400">{{ new Date(j.created_at).toLocaleString() }}</p></button><button v-if="j.status !== 'running'" type="button" class="mt-2 text-xs text-red-600 dark:text-red-400" :disabled="working || !!uncertainInput" @click="deleteJob(j)">{{ j.deleting ? t('imageWorkbench.retryDelete') : t('imageWorkbench.delete') }}</button></div><p v-if="!history.length" class="py-8 text-center text-sm text-gray-500">{{ t('imageWorkbench.noHistory') }}</p><button v-if="hasMore" type="button" class="btn btn-secondary w-full" :disabled="loadingHistory" @click="moreHistory">{{ t('imageWorkbench.more') }}</button></div></section></div>
    <div v-if="previewID && previews[previewID]" class="fixed inset-0 z-[60] flex items-center justify-center bg-black/90 p-4" role="dialog" aria-modal="true" :aria-label="t('imageWorkbench.preview')" @click.self="previewID = ''"><button type="button" class="absolute right-4 top-4 rounded-lg bg-white/20 px-3 py-2 text-white" :aria-label="t('imageWorkbench.close')" @click="previewID = ''">×</button><img :src="previews[previewID]" :alt="t('imageWorkbench.preview')" class="max-h-[90dvh] max-w-full object-contain" /></div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import * as keysAPI from '@/api/keys'
import { useModelSelectionPreference } from '@/composables/useModelSelectionPreference'
import { imageWorkbenchAPI as api, type WorkbenchAsset, type WorkbenchInput, type WorkbenchJob, type WorkbenchLimits, type WorkbenchProtocol } from '@/api/imageWorkbench'

const { t } = useI18n()
const keys = ref<{ id: number; name: string }[]>([]), keyID = ref<number | null>(null)
const protocol = ref<WorkbenchProtocol>('images'), nativeGemini = ref(false), models = ref<string[]>([]), model = ref(''), modelsLoading = ref(false)
const prompt = ref(''), count = ref(1), size = ref('auto'), quality = ref('auto'), ratio = ref('auto'), resolution = ref('auto')
const references = ref<WorkbenchAsset[]>([]), drafts = ref<WorkbenchAsset[]>([]), previews = ref<Record<string, string>>({}), previewID = ref('')
const job = ref<WorkbenchJob | null>(null), history = ref<WorkbenchJob[]>([]), historyOpen = ref(false), hasMore = ref(false), loadingHistory = ref(false)
const limits = ref<WorkbenchLimits | null>(null), error = ref(''), working = ref(false), loading = ref(true), uncertainInput = ref<WorkbenchInput | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)
const ratios = ['1:1', '2:3', '3:2', '3:4', '4:3', '4:5', '5:4', '9:16', '16:9', '21:9']
const fileLimit = 20 * 1024 * 1024
const running = computed(() => history.value.some(j => j.status === 'running') || job.value?.status === 'running')
const unusedDrafts = computed(() => drafts.value.filter(a => !references.value.some(r => r.id === a.id)))
const canGenerate = computed(() => !working.value && !loading.value && !modelsLoading.value && !running.value && !uncertainInput.value && !!keyID.value && models.value.includes(model.value) && !!prompt.value.trim() && !!limits.value && limits.value.used_bytes + limits.value.reserved_bytes + count.value * fileLimit <= limits.value.storage_bytes)
let disposed = false, page = 1, modelVersion = 0, detailVersion = 0, polling = false
let poll: ReturnType<typeof setInterval> | undefined, modelController: AbortController | undefined
const loadingPreviews = new Set<string>()

function showError(err: unknown) {
  error.value = err instanceof Error ? err.message : err && typeof err === 'object' && 'message' in err && typeof err.message === 'string' ? err.message : t('imageWorkbench.networkError')
}
const selectionPreference = useModelSelectionPreference('image-workbench', showError)
watch([keyID, model, protocol, modelsLoading], () => {
  if (!loading.value && !modelsLoading.value && !disposed && (!keyID.value || models.value.length)) selectionPreference.save(keyID.value, model.value, protocol.value)
})
async function loadPreview(a: WorkbenchAsset) {
  if (previews.value[a.id] || loadingPreviews.has(a.id)) return
  loadingPreviews.add(a.id)
  try { const blob = await api.download(a.id); if (!disposed && [...references.value, ...(job.value?.assets ?? [])].some(current => current.id === a.id)) previews.value[a.id] = URL.createObjectURL(blob) }
  catch (err) { if (!disposed) showError(err) }
  finally { loadingPreviews.delete(a.id) }
}
function prunePreviews() {
  const keep = new Set([...references.value, ...(job.value?.assets ?? [])].map(a => a.id))
  for (const [id, url] of Object.entries(previews.value)) if (!keep.has(id)) { URL.revokeObjectURL(url); delete previews.value[id] }
}
async function loadModels(preferred?: WorkbenchProtocol) {
  const version = ++modelVersion
  modelController?.abort(); modelController = new AbortController()
  models.value = []; nativeGemini.value = false
  if (!keyID.value) { model.value = ''; modelsLoading.value = false; return }
  modelsLoading.value = true
  try {
    const data = await api.models(keyID.value, preferred, modelController.signal)
    if (disposed || version !== modelVersion) return
    protocol.value = data.protocol; nativeGemini.value = data.native_gemini_available
    models.value = [...new Set(data.ids)].sort()
    if (!models.value.includes(model.value)) model.value = ''
  } catch (err) { if (!disposed && version === modelVersion && !modelController.signal.aborted) showError(err) }
  finally { if (version === modelVersion) modelsLoading.value = false }
}
watch(keyID, () => { if (!loading.value) void loadModels() })
function changeProtocol(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (value === 'images' || value === 'gemini') void loadModels(value)
}
async function refreshHistory() { history.value = await api.list(); page = 1; hasMore.value = history.value.length === 30 }
async function openHistory() { historyOpen.value = true; try { await refreshHistory() } catch (err) { showError(err) } }
async function moreHistory() {
  loadingHistory.value = true
  try { const items = await api.list(page + 1); history.value.push(...items.filter(j => !history.value.some(old => old.id === j.id))); page++; hasMore.value = items.length === 30 }
  catch (err) { showError(err) } finally { loadingHistory.value = false }
}
async function selectJob(id: string) {
  const version = ++detailVersion
  try { const data = await api.get(id); if (disposed || version !== detailVersion) return; job.value = data; historyOpen.value = false; prunePreviews(); await Promise.all(data.assets.map(loadPreview)) }
  catch (err) { if (!disposed) showError(err) }
}
async function uploadFiles(files: File[]) {
  if (working.value || files.length === 0) return
  if (references.value.length + files.length > 5) { error.value = t('imageWorkbench.referenceCount'); return }
  working.value = true; error.value = ''
  try {
    for (const f of files) {
      if (!['image/png', 'image/jpeg', 'image/webp'].includes(f.type) || !f.size || f.size > fileLimit) throw new Error(t('imageWorkbench.fileError'))
      const a = await api.upload(f); drafts.value.push(a); references.value.push(a); previews.value[a.id] = URL.createObjectURL(f)
      limits.value = await api.limits()
    }
  } catch (err) { showError(err) } finally { working.value = false; if (fileInput.value) fileInput.value.value = '' }
}
function closeDialog(event: KeyboardEvent) { if (event.key === 'Escape') { previewID.value = ''; historyOpen.value = false } }
function selectFiles(event: Event) { void uploadFiles(Array.from((event.target as HTMLInputElement).files ?? [])) }
function dropFiles(event: DragEvent) { void uploadFiles(Array.from(event.dataTransfer?.files ?? [])) }
function pasteFiles(event: ClipboardEvent) { const files = Array.from(event.clipboardData?.files ?? []); if (files.length) { event.preventDefault(); void uploadFiles(files) } }
async function removeReference(a: WorkbenchAsset) {
  working.value = true
  try { if (a.draft) { await api.removeAsset(a.id); drafts.value = drafts.value.filter(x => x.id !== a.id) }
    references.value = references.value.filter(x => x.id !== a.id); prunePreviews(); limits.value = await api.limits() }
  catch (err) { showError(err) } finally { working.value = false }
}
async function addDraftReference(a: WorkbenchAsset) {
  if (references.value.length >= 5 || references.value.some(r => r.id === a.id)) return
  references.value.push(a); await loadPreview(a)
}
function input(): WorkbenchInput {
  if (!keyID.value) throw new Error(t('imageWorkbench.selectKey'))
  return { request_id: crypto.randomUUID(), api_key_id: keyID.value, protocol: protocol.value, model: model.value, prompt: prompt.value.trim(), count: count.value, size: protocol.value === 'images' ? size.value : '', quality: protocol.value === 'images' ? quality.value : '', aspect_ratio: protocol.value === 'gemini' ? ratio.value : '', image_size: protocol.value === 'gemini' ? resolution.value : '', reference_ids: references.value.map(a => a.id) }
}
async function submit(incoming: WorkbenchInput) {
  working.value = true; error.value = ''; uncertainInput.value = incoming
  try {
    const result = await api.submit(incoming)
    if (disposed) return
    uncertainInput.value = null; ++detailVersion; job.value = result.job
    for (const a of references.value) if (incoming.reference_ids.includes(a.id)) a.draft = false
    drafts.value = drafts.value.filter(a => !incoming.reference_ids.includes(a.id))
    await refreshHistory(); limits.value = await api.limits(); await selectJob(result.job.id)
  } catch (err) {
    if (err && typeof err === 'object' && 'status' in err && typeof err.status === 'number' && err.status >= 400 && err.status < 500) uncertainInput.value = null
    if (!disposed) showError(err)
  } finally { working.value = false }
}
function generate() { if (canGenerate.value) void submit(input()) }
function resubmit() { if (uncertainInput.value) void submit(uncertainInput.value) }
async function cancelJob() {
  if (!job.value) return
  working.value = true
  try { await api.cancel(job.value.id); error.value = t('imageWorkbench.cancelling') }
  catch (err) { showError(err) } finally { working.value = false }
}
async function deleteJob(j: WorkbenchJob) {
  if (!window.confirm(t('imageWorkbench.deleteConfirm'))) return
  working.value = true
  try {
    const detail = job.value?.id === j.id ? job.value : await api.get(j.id)
    await api.remove(j.id)
    const assetIDs = new Set(detail.assets.map(a => a.id))
    references.value = references.value.filter(a => !assetIDs.has(a.id))
    if (job.value?.id === j.id) { ++detailVersion; job.value = null }
    await refreshHistory(); limits.value = await api.limits(); prunePreviews()
  } catch (err) { showError(err) } finally { working.value = false }
}
async function reuseJob(j: WorkbenchJob) {
  working.value = true
  try {
    const detail = await api.get(j.id)
    prompt.value = j.input.prompt; count.value = j.input.count
    size.value = j.input.size || 'auto'; quality.value = j.input.quality || 'auto'; ratio.value = j.input.aspect_ratio || 'auto'; resolution.value = j.input.image_size || 'auto'
    references.value = j.input.reference_ids.map(id => detail.assets.find(a => a.id === id)).filter((a): a is WorkbenchAsset => !!a)
    keyID.value = j.api_key_id && keys.value.some(k => k.id === j.api_key_id) ? j.api_key_id : null
    await nextTick()
    model.value = j.input.model
    await loadModels(j.input.protocol); prunePreviews(); await Promise.all(references.value.map(loadPreview))
  } catch (err) { showError(err) } finally { working.value = false }
}
async function addResultReference(id: string) {
  const a = job.value?.assets.find(a => a.id === id)
  if (!a || references.value.some(x => x.id === id) || references.value.length >= 5) return
  references.value.push(a); await loadPreview(a)
}
async function download(id: string) {
  try {
    const a = job.value?.assets.find(a => a.id === id), blob = await api.download(id), url = URL.createObjectURL(blob)
    const link = document.createElement('a'); link.href = url; link.download = a?.filename ?? 'image.png'; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (err) { showError(err) }
}
async function pollJobs() {
  if (polling || disposed || working.value || !running.value) return
  polling = true
  try {
    const id = job.value?.id, version = detailVersion
    if (id) { const latest = await api.get(id); if (!disposed && version === detailVersion) { job.value = latest; await Promise.all(latest.assets.map(loadPreview)) } }
    if (!disposed) { await refreshHistory(); limits.value = await api.limits() }
  } catch (err) { if (!disposed) showError(err) } finally { polling = false }
}
onMounted(async () => {
  window.addEventListener('keydown', closeDialog)
  try {
    const all: { id: number; name: string }[] = []
    for (let n = 1; ; n++) { const data = await keysAPI.list(n, 100, { status: 'active' }); all.push(...data.items.filter(k => k.status === 'active').map(k => ({ id: k.id, name: k.name }))); if (n >= data.pages) break }
    if (disposed) return
    keys.value = all
    const saved = selectionPreference.load()
    if (saved && all.some(k => k.id === saved.keyID)) {
      keyID.value = saved.keyID; model.value = saved.model
      await nextTick()
      await loadModels(saved.protocol)
    }
    limits.value = await api.limits(); drafts.value = await api.drafts(); references.value = drafts.value.slice(0, 5)
    await refreshHistory(); await Promise.all(references.value.map(loadPreview))
    const current = history.value.find(j => j.status === 'running') ?? history.value[0]
    if (current) await selectJob(current.id)
    poll = setInterval(() => { void pollJobs() }, 3000)
  } catch (err) { if (!disposed) showError(err) } finally { loading.value = false }
})
onBeforeUnmount(() => { window.removeEventListener('keydown', closeDialog); disposed = true; modelController?.abort(); if (poll) clearInterval(poll); for (const url of Object.values(previews.value)) URL.revokeObjectURL(url) })
</script>
