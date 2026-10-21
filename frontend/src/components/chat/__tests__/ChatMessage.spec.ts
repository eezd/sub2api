import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ChatMessageCard from '../ChatMessage.vue'
import type { ChatMessage } from '@/api/chat'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('Chat message rendering', () => {
  it('renders Markdown while stripping scripts, event handlers and remote images', () => {
    const message = { role: 'assistant', model: 'model', content: '**strong** <img src="https://tracker.invalid/pixel" onerror="alert(1)"><script>alert(1)</script><svg><image href="https://tracker.invalid/svg" /></svg><table background="https://tracker.invalid/bg"></table>[bad](javascript:alert(1))', reasoning: 'plain <script>text</script>', created_at: new Date().toISOString(), status: 'complete' } as ChatMessage
    const wrapper = mount(ChatMessageCard, { props: { message, attachments: [], previews: {} } })
    expect(wrapper.find('strong').text()).toBe('strong')
    expect(wrapper.find('img').exists()).toBe(false); expect(wrapper.find('script').exists()).toBe(false); expect(wrapper.find('svg').exists()).toBe(false); expect(wrapper.find('[background]').exists()).toBe(false)
    expect(wrapper.find('[onerror]').exists()).toBe(false); expect(wrapper.find('[href^="javascript:"]').exists()).toBe(false)
    expect(wrapper.get('details').text()).toContain('plain <script>text</script>')
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
  })
})
