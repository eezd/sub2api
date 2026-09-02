import type * as VueI18n from 'vue-i18n'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import InfiniteCanvasView from '../InfiniteCanvasView.vue'

const listKeys = vi.hoisted(() => vi.fn())
const publicSettings = vi.hoisted(() => ({ api_base_url: '/api/v1', infinite_canvas_url: '' }))

vi.mock('@/api/keys', () => ({
  keysAPI: { list: listKeys }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: publicSettings
  })
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof VueI18n>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

describe('InfiniteCanvasView', () => {
  beforeEach(() => {
    listKeys.mockReset()
    publicSettings.infinite_canvas_url = ''
    vi.stubEnv('VITE_INFINITE_CANVAS_URL', '')
  })

  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('prefers the runtime canvas URL over the build-time fallback', async () => {
    publicSettings.infinite_canvas_url = 'https://runtime.example.com/canvas'
    vi.stubEnv('VITE_INFINITE_CANVAS_URL', 'https://build.example.com/canvas')
    listKeys.mockResolvedValue({ items: [{ id: 1, key: 'active-key', status: 'active' }] })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()
    expect(new URL(wrapper.get('iframe').attributes('src')!).origin).toBe('https://runtime.example.com')
  })

  it('uses the build-time canvas URL when runtime configuration is empty', async () => {
    vi.stubEnv('VITE_INFINITE_CANVAS_URL', 'https://build.example.com/canvas')
    listKeys.mockResolvedValue({ items: [{ id: 1, key: 'active-key', status: 'active' }] })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()
    expect(new URL(wrapper.get('iframe').attributes('src')!).origin).toBe('https://build.example.com')
  })

  it('injects the first active API key into the canvas URL', async () => {
    listKeys.mockResolvedValue({
      items: [
        { id: 1, key: 'inactive-key', status: 'inactive' },
        { id: 2, key: 'active-key', status: 'active' }
      ]
    })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()
    const frame = wrapper.get('iframe')
    const frameUrl = new URL(frame.attributes('src'), window.location.origin)
    expect(frameUrl.searchParams.get('baseUrl')).toBe(`${window.location.origin}/api/v1`)
    expect(frameUrl.searchParams.get('apiKey')).toBe('active-key')
    expect(wrapper.find('header.canvas-toolbar').exists()).toBe(true)
    expect(wrapper.get('select').text()).toContain('1 · activ...-key')
  })

  it('reloads the canvas with the selected active API key', async () => {
    listKeys.mockResolvedValue({
      items: [
        { id: 1, key: 'sk-first-key', status: 'active' },
        { id: 2, key: 'sk-second-key', status: 'active' }
      ]
    })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()
    await wrapper.get('select').setValue(2)
    await nextTick()

    const frameUrl = new URL(wrapper.get('iframe').attributes('src'), window.location.origin)
    expect(frameUrl.searchParams.get('apiKey')).toBe('sk-second-key')
  })
  it('shows a configuration error instead of embedding the current page', async () => {
    publicSettings.infinite_canvas_url = `${window.location.origin}/infinite-canvas`
    const originalPath = window.location.pathname
    window.history.pushState({}, '', '/infinite-canvas')
    listKeys.mockResolvedValue({ items: [{ id: 1, key: 'active-key', status: 'active' }] })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('infiniteCanvas.notConfigured')
    window.history.pushState({}, '', originalPath)
  })

  it('shows the key management action when no active key exists', async () => {
    listKeys.mockResolvedValue({ items: [] })

    const wrapper = mount(InfiniteCanvasView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          LoadingSpinner: true,
          RouterLink: { template: '<a><slot /></a>' }
        }
      }
    })

    await flushPromises()

    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('infiniteCanvas.openKeys')
  })
})
