<template>
  <article class="rounded-xl border p-4" :class="message.role === 'user' ? 'border-primary-100 bg-primary-50/60 dark:border-primary-900 dark:bg-primary-950/20' : 'border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900'">
    <div class="mb-3 flex items-center justify-between gap-2 text-xs text-gray-500">
      <span>{{ message.role === 'user' ? t('chat.you') : message.model }} · {{ new Date(message.created_at).toLocaleString() }}</span>
      <button type="button" class="btn btn-ghost px-2 py-1" @click="copy">{{ copied ? t('chat.copied') : t('chat.copy') }}</button>
    </div>
    <details v-if="message.reasoning" class="mb-3 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">
      <summary class="cursor-pointer text-gray-500">{{ t('chat.reasoning') }}</summary>
      <p class="mt-2 whitespace-pre-wrap break-words">{{ message.reasoning }}</p>
    </details>
    <div v-if="message.role === 'assistant'" class="chat-markdown break-words" v-html="html"></div>
    <p v-else class="whitespace-pre-wrap break-words">{{ message.content }}</p>
    <div v-if="attachments.length" class="mt-3 flex flex-wrap gap-2">
      <button v-for="a in attachments" :key="a.id" type="button" class="max-w-[200px] overflow-hidden rounded-lg border border-gray-200 p-2 text-left text-xs dark:border-dark-700" @click="$emit('download', a)">
        <img v-if="previews[a.id]" :src="previews[a.id]" :alt="a.filename" class="mb-1 max-h-40 rounded object-contain" />
        <span class="block truncate">{{ a.filename }}</span>
      </button>
    </div>
    <p v-if="message.status === 'generating'" role="status" class="mt-3 animate-pulse text-xs text-primary-600">{{ t('chat.generating') }}</p>
    <p v-if="message.status === 'failed' || message.status === 'interrupted'" class="mt-3 text-sm text-amber-700 dark:text-amber-400">{{ message.error || t('chat.interrupted') }}</p>
    <p v-if="copyError" role="alert" class="mt-2 text-xs text-red-600">{{ t('chat.copyFailed') }}</p>
  </article>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import type { ChatAttachment, ChatMessage } from '@/api/chat'

const props = defineProps<{ message: ChatMessage; attachments: ChatAttachment[]; previews: Record<string, string> }>()
defineEmits<{ download: [attachment: ChatAttachment] }>()
const { t } = useI18n()
const copied = ref(false), copyError = ref(false)
const html = computed(() => DOMPurify.sanitize(marked.parse(props.message.content, { async: false }), {
  // 模型不能通过远程图片向第三方泄露用户 IP；用户附件单独通过登录接口展示。
  USE_PROFILES: { html: true },
  FORBID_TAGS: ['img', 'iframe', 'form', 'input', 'video', 'audio', 'style'], FORBID_ATTR: ['style', 'background', 'src', 'srcset']
}))
async function copy() {
  try { await navigator.clipboard.writeText(props.message.content); copied.value = true; copyError.value = false }
  catch { copyError.value = true }
}
</script>

<style scoped>
.chat-markdown :deep(p), .chat-markdown :deep(ul), .chat-markdown :deep(ol) { margin: .6rem 0; }
.chat-markdown :deep(ul) { list-style: disc; padding-left: 1.5rem; }
.chat-markdown :deep(ol) { list-style: decimal; padding-left: 1.5rem; }
.chat-markdown :deep(pre) { overflow-x: auto; padding: 1rem; border-radius: .5rem; background: #111827; color: #f3f4f6; }
.chat-markdown :deep(code) { font-size: .875em; }
.chat-markdown :deep(table) { display: block; overflow-x: auto; border-collapse: collapse; }
.chat-markdown :deep(td), .chat-markdown :deep(th) { border: 1px solid #9ca3af; padding: .4rem; }
.chat-markdown :deep(a) { color: #2563eb; text-decoration: underline; }
.chat-markdown :deep(blockquote) { border-left: 3px solid #9ca3af; padding-left: 1rem; opacity: .8; }
</style>
