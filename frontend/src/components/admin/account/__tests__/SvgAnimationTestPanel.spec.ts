import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import SvgAnimationTestPanel from '../SvgAnimationTestPanel.vue'
import SvgAnimationResultPreview from '../SvgAnimationResultPreview.vue'

const { getDegradationCheckHistory } = vi.hoisted(() => ({
  getDegradationCheckHistory: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { getDegradationCheckHistory }
  }
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal()
  return {
    ...(actual as object),
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) =>
        params?.count === undefined ? key : `${key}:${params.count}`
    })
  }
})

function createStreamResponse(events: Array<Record<string, unknown>>) {
  const encoder = new TextEncoder()
  const chunks = events.map((event) => encoder.encode(`data: ${JSON.stringify(event)}\n`))
  let index = 0

  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) return { done: false, value: chunks[index++] }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountPanel() {
  return mount(SvgAnimationTestPanel, {
    props: {
      active: true,
      accountId: 42,
      models: [{ id: 'gpt-5.4', display_name: 'GPT-5.4' }] as any
    },
    global: {
      stubs: {
        Select: { template: '<div class="select-stub"></div>' },
        Icon: true
      }
    }
  })
}

describe('SvgAnimationTestPanel', () => {
  beforeEach(() => {
    getDegradationCheckHistory.mockReset()
    getDegradationCheckHistory.mockResolvedValue([])
    Object.defineProperty(globalThis, 'localStorage', {
      value: { getItem: vi.fn(() => 'test-token') },
      configurable: true
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('uses the dedicated endpoint and renders its persisted SVG result in a restricted sandbox', async () => {
    const generated = [
      '```html\n<!doctype html><html><head>',
      '<script>window.parent.postMessage("unsafe", "*")</script></head>',
      '<body><svg id="pelican-bike" viewBox="0 0 100 100">',
      '<circle cx="50" cy="70" r="18"><animate attributeName="cx" values="45;55;45" dur="1s" repeatCount="indefinite" /></circle>',
      '</svg></body></html>\n```'
    ].join('')
    const result = {
      id: 7,
      account_id: 42,
      check_type: 'svg_animation',
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      status: 'success',
      result: {},
      output_text: generated,
      created_at: '2026-09-29T12:00:00Z'
    }
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(createStreamResponse([
      { type: 'svg_animation_start', model: 'gpt-5.4' },
      { type: 'svg_animation_complete', success: true, data: result }
    ]))
    global.fetch = fetchMock

    const wrapper = mountPanel()
    const vm = wrapper.vm as unknown as { selectedModelId: string; start: () => Promise<void> }
    vm.selectedModelId = 'gpt-5.4'
    await vm.start()
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, request] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/42/svg-animation-test')
    expect(JSON.parse(String(request?.body))).toEqual({ model_id: 'gpt-5.4' })

    const frame = wrapper.get('[data-test="svg-animation-frame"]')
    const source = frame.attributes('srcdoc')
    expect(frame.attributes('sandbox')).toBe('')
    expect(source).toContain('Content-Security-Policy')
    expect(source).toContain('id="pelican-bike"')
    expect(source).toContain('<animate')
    expect(source).not.toContain('<script')
  })

  it('reports a model response that contains no SVG instead of showing an empty preview', async () => {
    global.fetch = vi.fn().mockResolvedValue(createStreamResponse([{
      type: 'svg_animation_complete',
      success: true,
      data: {
        id: 8,
        account_id: 42,
        check_type: 'svg_animation',
        requested_model: 'gpt-5.4',
        tested_model: 'gpt-5.4',
        status: 'success',
        result: {},
        output_text: '<html><body>plain text only</body></html>',
        created_at: '2026-09-29T12:01:00Z'
      }
    }])) as any

    const wrapper = mountPanel()
    const vm = wrapper.vm as unknown as { selectedModelId: string; start: () => Promise<void> }
    vm.selectedModelId = 'gpt-5.4'
    await vm.start()
    await flushPromises()

    expect(wrapper.get('[data-test="svg-animation-error"]').text()).toContain(
      'admin.accounts.svgAnimationTest.noSvg'
    )
    expect(wrapper.find('[data-test="svg-animation-frame"]').exists()).toBe(false)
  })

  it('loads persisted history and replays a successful prior SVG result', async () => {
    const historical = {
      id: 11,
      account_id: 42,
      check_type: 'svg_animation',
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      status: 'success',
      result: {},
      output_text: '<svg id="historical-pelican"><animate attributeName="x" values="0;1" /></svg>',
      created_at: '2026-09-28T12:00:00Z'
    }
    getDegradationCheckHistory.mockResolvedValue([historical])

    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.get('[data-test="svg-animation-history"] button').trigger('click')

    expect(getDegradationCheckHistory).toHaveBeenCalledWith(42, 'svg_animation', 10)
    expect(wrapper.get('[data-test="svg-animation-frame"]').attributes('srcdoc')).toContain('id="historical-pelican"')
  })

  it.each([
    ['a late <head> after a remote image', '<!doctype html><html><img src="https://attacker.test/a"><head></head><body><svg id="late"></svg></body></html>'],
    ['a <head> that only appears inside a comment', '<html><body><img src="https://attacker.test/b"><svg id="commented"></svg><!-- <head> --></body></html>'],
    ['bare SVG markup', '<svg id="bare"><image href="https://attacker.test/c" /></svg>'],
    ['a regular complete document', '<!doctype html><html><head><style>svg{}</style></head><body><svg id="regular"></svg></body></html>']
  ])('puts the preview CSP first in the document for %s', async (_label, outputText) => {
    const wrapper = mount(SvgAnimationResultPreview, { props: { outputText } })
    const parsed = new DOMParser().parseFromString(wrapper.get('[data-test="svg-animation-frame"]').attributes('srcdoc') ?? '', 'text/html')
    const policies = parsed.querySelectorAll('meta[http-equiv="Content-Security-Policy"]')
    expect(policies).toHaveLength(1)
    expect(parsed.head.firstElementChild).toBe(policies[0])
    expect(policies[0].getAttribute('content')).toContain("default-src 'none'")
    expect(wrapper.get('[data-test="svg-animation-frame"]').attributes('sandbox')).toBe('')
    expect(wrapper.get('[data-test="svg-animation-frame"]').attributes('referrerpolicy')).toBe('no-referrer')
    expect(parsed.querySelector('svg')).not.toBeNull()
  })

  it('removes executable markup and model policies without removing SVG animations', () => {
    const wrapper = mount(SvgAnimationResultPreview, {
      props: {
        outputText: '<html><head><base href="https://attacker.test/"><meta http-equiv="refresh" content="0;url=https://attacker.test/"><meta http-equiv="Content-Security-Policy" content="default-src *"><script>parent.location="https://attacker.test/"</script><script href="https://attacker.test/run" /></head><body><svg onload="alert(1)"><style>circle{animation:pulse 1s infinite}</style><circle><animate attributeName="r" values="1;2;1" dur="1s" /></circle><image href="https://attacker.test/image" /></svg></body></html>'
      }
    })
    const source = wrapper.get('[data-test="svg-animation-frame"]').attributes('srcdoc') || ''
    const parsed = new DOMParser().parseFromString(source, 'text/html')
    expect(parsed.querySelector('script, base, meta[http-equiv="refresh"]')).toBeNull()
    expect(parsed.querySelectorAll('meta[http-equiv="Content-Security-Policy"]')).toHaveLength(1)
    expect(parsed.head.firstElementChild?.getAttribute('content')).toBe("default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline';")
    expect(parsed.querySelector('animate')?.getAttribute('values')).toBe('1;2;1')
    expect(parsed.querySelector('style')?.textContent).toContain('animation:pulse')
    expect(wrapper.find('svg, script, style, base').exists()).toBe(false)
  })

  it('keeps a newer run running when an older aborted run settles, and locks history meanwhile', async () => {
    getDegradationCheckHistory.mockResolvedValue([{
      id: 13,
      account_id: 42,
      check_type: 'svg_animation',
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      status: 'success',
      result: {},
      output_text: '<svg id="older"></svg>',
      created_at: '2026-09-28T12:00:00Z'
    }])
    let finishSecondRun: () => void = () => {}
    const secondRunDone = new Promise<void>((resolve) => { finishSecondRun = resolve })
    const encoder = new TextEncoder()
    const completeEvent = {
      type: 'svg_animation_complete',
      success: true,
      data: { id: 14, account_id: 43, check_type: 'svg_animation', requested_model: 'gpt-5.4', tested_model: 'gpt-5.4', status: 'success', result: {}, output_text: '<svg id="newer"></svg>', created_at: '2026-09-29T12:00:00Z' }
    }
    let secondReads = 0
    let settleFirstRun: () => void = () => {}
    global.fetch = vi.fn()
      .mockImplementationOnce((_url, init?: RequestInit) => new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          settleFirstRun = () => reject(new DOMException('Aborted', 'AbortError'))
        })
      }))
      .mockImplementationOnce(async () => ({
        ok: true,
        body: {
          getReader: () => ({
            read: async () => {
              secondReads += 1
              if (secondReads === 1) {
                await secondRunDone
                return { done: false, value: encoder.encode(`data: ${JSON.stringify(completeEvent)}\n`) }
              }
              return { done: true, value: undefined }
            }
          })
        }
      })) as any

    const wrapper = mountPanel()
    await flushPromises()
    const vm = wrapper.vm as unknown as { selectedModelId: string; status: string; start: () => Promise<void> }
    vm.selectedModelId = 'gpt-5.4'
    const firstRun = vm.start()
    await flushPromises()
    expect(wrapper.get('[data-test="svg-animation-history"] button').attributes('disabled')).toBeDefined()

    await wrapper.setProps({ accountId: 43 })
    vm.selectedModelId = 'gpt-5.4'
    const secondRun = vm.start()
    await flushPromises()
    settleFirstRun()
    await firstRun
    await flushPromises()
    expect(vm.status).toBe('running')

    finishSecondRun()
    await secondRun
    await flushPromises()
    expect(vm.status).toBe('success')
    expect(wrapper.get('[data-test="svg-animation-frame"]').attributes('srcdoc')).toContain('id="newer"')
  })
})
