import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountTestModal from '../AccountTestModal.vue'
import ModelTraceResultPanel from '../ModelTraceResultPanel.vue'
import type { ModelTraceResult } from '@/utils/degradationChecks'

const { getAvailableModels, getDegradationCheckHistory, copyToClipboard } = vi.hoisted(() => ({
  getAvailableModels: vi.fn(),
  getDegradationCheckHistory: vi.fn(),
  copyToClipboard: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getAvailableModels,
      getDegradationCheckHistory
    }
  }
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.accounts.imagePromptDefault': 'Generate a cute orange cat astronaut sticker on a clean pastel background.'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'admin.accounts.imageReceived' && params?.count) {
          return `received-${params.count}`
        }
        if (key === 'admin.accounts.imagePreviewAlt' && params?.index) {
          return `test-image-${params.index}`
        }
        return messages[key] || key
      }
    })
  }
})

function createStreamResponse(lines: string[]) {
  const encoder = new TextEncoder()
  const chunks = lines.map((line) => encoder.encode(line))
  let index = 0

  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn().mockImplementation(async () => {
          if (index < chunks.length) {
            return { done: false, value: chunks[index++] }
          }
          return { done: true, value: undefined }
        })
      })
    }
  } as Response
}

function mountModal(account: Record<string, unknown> = {
  id: 42,
  name: 'Gemini Image Test',
  platform: 'gemini',
  type: 'apikey',
  status: 'active'
}) {
  return mount(AccountTestModal, {
    props: {
      show: false,
      account
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Select: { template: '<div class="select-stub"></div>' },
        TextArea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea class="textarea-stub" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        Icon: true
      }
    }
  })
}

describe('AccountTestModal', () => {
  beforeEach(() => {
    getAvailableModels.mockResolvedValue([
      { id: 'gemini-2.0-flash', display_name: 'Gemini 2.0 Flash' },
      { id: 'gemini-2.5-flash-image', display_name: 'Gemini 2.5 Flash Image' },
      { id: 'gemini-3.1-flash-image', display_name: 'Gemini 3.1 Flash Image' }
    ])
    getDegradationCheckHistory.mockReset()
    getDegradationCheckHistory.mockResolvedValue([])
    copyToClipboard.mockReset()
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: vi.fn((key: string) => (key === 'auth_token' ? 'test-token' : null)),
        setItem: vi.fn(),
        removeItem: vi.fn(),
        clear: vi.fn()
      },
      configurable: true
    })
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"gemini-2.5-flash-image"}\n',
        'data: {"type":"image","image_url":"data:image/png;base64,QUJD","mime_type":"image/png"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('gemini 图片模型测试会携带提示词并渲染图片预览', async () => {
    const wrapper = mountModal()
    await wrapper.setProps({ show: true })
    await flushPromises()

    const promptInput = wrapper.find('textarea.textarea-stub')
    expect(promptInput.exists()).toBe(true)
    await promptInput.setValue('draw a tiny orange cat astronaut')

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'gemini-3.1-flash-image',
      prompt: 'draw a tiny orange cat astronaut'
    })

    const preview = wrapper.find('img[alt="test-image-1"]')
    expect(preview.exists()).toBe(true)
    expect(preview.attributes('src')).toBe('data:image/png;base64,QUJD')
  })

  it('grok 账号测试默认选择 Grok 模型', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'grok-4.3', display_name: 'Grok 4.3' },
      { id: 'grok-build-0.1', display_name: 'Grok Build 0.1' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_start","model":"grok-4.3"}\n',
        'data: {"type":"content","text":"ok"}\n',
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 13,
      name: 'Grok Account',
      platform: 'grok',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const buttons = wrapper.findAll('button')
    const startButton = buttons.find((button) => button.text().includes('admin.accounts.startTest'))
    expect(startButton).toBeTruthy()

    await startButton!.trigger('click')
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toEqual({
      model_id: 'grok-4.3',
      prompt: '',
      mode: 'text'
    })
  })

  it('OpenAI Compact 探测会携带 compact 测试模式', async () => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' }
    ])
    global.fetch = vi.fn().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"test_complete","success":true}\n'
      ])
    ) as any

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    ;(wrapper.vm as any).selectedModelId = 'gpt-5.4'
    ;(wrapper.vm as any).testMode = 'compact'
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(1)
    const [, request] = (global.fetch as any).mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({
      model_id: 'gpt-5.4',
      prompt: '',
      mode: 'compact'
    })
  })

  it.each([
    [false, 'mismatch'],
    [null, 'unknown']
  ] as const)('ModelTrace requires a selected model and keeps %s attribution execution successful', async (matchesExpected, verdict) => {
    getAvailableModels.mockResolvedValue([
      { id: 'gpt-5.4', display_name: 'GPT-5.4' },
      { id: 'gpt-image-2', display_name: 'GPT Image 2' }
    ])
    const result = {
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      expected_model_in_bank: matchesExpected !== null,
      matches_expected: matchesExpected,
      prediction: 'claude-opus-4-6',
      prediction_name: 'claude-opus-4-6',
      probability: 0.72,
      family_prediction_name: 'Claude',
      family_probability: 0.81,
      used_outputs: 3,
      results: [
        { model: 'claude-opus-4-6', display_name: 'claude-opus-4-6', family_name: 'Claude', probability: 0.72 },
        { model: 'gpt-5.4', display_name: 'gpt-5.4', family_name: 'GPT', probability: 0.2 }
      ],
      disclaimer: 'Closed-set statistical attribution.'
    }
    const modelTraceFetch = vi.fn<typeof fetch>().mockResolvedValue(
      createStreamResponse([
        'data: {"type":"model_trace_progress","data":{"attempt":1,"max_attempts":6,"received":1,"target":3,"accepted":true,"parsed_numbers":220,"minimum_numbers":170}}\n',
        `data: ${JSON.stringify({ type: 'model_trace_complete', success: true, data: result })}\n`
      ])
    )
    global.fetch = modelTraceFetch

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const vm = wrapper.vm as unknown as { modelTraceModelId: string; startModelTrace: () => Promise<void> }
    expect(vm.modelTraceModelId).toBe('')
    expect(wrapper.find('[data-test="model-trace-start"]').attributes('disabled')).toBeDefined()

    vm.modelTraceModelId = 'gpt-5.4'
    await vm.startModelTrace()
    await flushPromises()

    expect(modelTraceFetch).toHaveBeenCalledTimes(1)
    const [url, request] = modelTraceFetch.mock.calls[0]
    expect(String(url)).toContain('/admin/accounts/42/model-trace')
    expect(JSON.parse(String(request?.body))).toEqual({ model_id: 'gpt-5.4' })
    expect(wrapper.text()).toContain(`admin.accounts.modelTrace.${verdict}`)
    expect(wrapper.vm).toHaveProperty('modelTraceStatus', 'success')
    expect(wrapper.text()).toContain('claude-opus-4-6')
  })

  it('加载 ModelTrace 历史并可恢复完整结果', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.4', display_name: 'GPT-5.4' }])
    const historicalResult = {
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      expected_model_in_bank: true,
      matches_expected: true,
      prediction: 'gpt-5.4',
      prediction_name: 'GPT-5.4',
      probability: 0.91,
      family_prediction_name: 'GPT',
      family_probability: 0.95,
      used_outputs: 3,
      results: [{ model: 'gpt-5.4', display_name: 'GPT-5.4', family_name: 'GPT', probability: 0.91 }],
      disclaimer: 'Historical result.'
    }
    getDegradationCheckHistory.mockImplementation(async (_id: number, type: string) => type === 'model_trace'
      ? [{
          id: 21,
          account_id: 42,
          check_type: 'model_trace',
          requested_model: 'gpt-5.4',
          tested_model: 'gpt-5.4',
          status: 'success',
          result: historicalResult,
          created_at: '2026-09-28T12:00:00Z'
        }]
      : [])

    const wrapper = mountModal({
      id: 42,
      name: 'OpenAI OAuth',
      platform: 'openai',
      type: 'oauth',
      status: 'active'
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(getDegradationCheckHistory).toHaveBeenCalledWith(42, 'model_trace', 10)
    expect(wrapper.get('[data-test="model-trace-history"]').text()).toContain('GPT-5.4')
    await wrapper.get('[data-test="model-trace-history"] button').trigger('click')
    expect(wrapper.get('[data-test="model-trace-result"]').text()).toContain('GPT-5.4')
    expect(wrapper.get('[data-test="model-trace-result"]').text()).toContain('91.0%')
  })

  it('ModelTrace 运行中锁定历史，旧请求中断后不会把新检测改回空闲', async () => {
    getAvailableModels.mockResolvedValue([{ id: 'gpt-5.4', display_name: 'GPT-5.4' }])
    const traceResult = {
      requested_model: 'gpt-5.4',
      tested_model: 'gpt-5.4',
      expected_model_in_bank: true,
      matches_expected: true,
      prediction: 'gpt-5.4',
      prediction_name: 'GPT-5.4',
      probability: 0.88,
      family_prediction_name: 'GPT',
      family_probability: 0.9,
      used_outputs: 3,
      results: [{ model: 'gpt-5.4', display_name: 'GPT-5.4', family_name: 'GPT', probability: 0.88 }],
      disclaimer: 'Fresh result.'
    }
    getDegradationCheckHistory.mockImplementation(async (_id: number, type: string) => type === 'model_trace'
      ? [{ id: 31, account_id: 42, check_type: 'model_trace', requested_model: 'gpt-5.4', tested_model: 'gpt-5.4', status: 'success', result: traceResult, created_at: '2026-09-28T12:00:00Z' }]
      : [])
    let finishSecondRun: () => void = () => {}
    const secondRunDone = new Promise<void>((resolve) => { finishSecondRun = resolve })
    const encoder = new TextEncoder()
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
                return { done: false, value: encoder.encode(`data: ${JSON.stringify({ type: 'model_trace_complete', success: true, data: traceResult })}\n`) }
              }
              return { done: true, value: undefined }
            }
          })
        }
      })) as any

    const wrapper = mountModal({ id: 42, name: 'OpenAI OAuth', platform: 'openai', type: 'oauth', status: 'active' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const vm = wrapper.vm as unknown as { modelTraceModelId: string; modelTraceStatus: string; startModelTrace: () => Promise<void> }
    vm.modelTraceModelId = 'gpt-5.4'
    const firstRun = vm.startModelTrace()
    await flushPromises()
    expect(wrapper.get('[data-test="model-trace-history"] button').attributes('disabled')).toBeDefined()

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    vm.modelTraceModelId = 'gpt-5.4'
    const secondRun = vm.startModelTrace()
    await flushPromises()
    settleFirstRun()
    await firstRun
    await flushPromises()
    expect(vm.modelTraceStatus).toBe('running')

    finishSecondRun()
    await secondRun
    await flushPromises()
    expect(vm.modelTraceStatus).toBe('success')
    expect(wrapper.get('[data-test="model-trace-result"]').text()).toContain('88.0%')
  })

  it.each([
    [true, 'match'],
    [false, 'mismatch'],
    [null, 'unknown']
  ] as const)('renders attribution %s independently of execution status', (matchesExpected, verdict) => {
    const result: ModelTraceResult = {
      requested_model: 'requested-public-id',
      tested_model: 'resolved-model',
      expected_model_in_bank: matchesExpected !== null,
      matches_expected: matchesExpected,
      prediction: 'first',
      prediction_name: 'First candidate',
      probability: 0.725,
      family_prediction_name: 'Family A',
      family_probability: 0.8,
      used_outputs: 3,
      results: [
        { model: 'first', display_name: 'First candidate', family_name: 'Family A', probability: 0.725 },
        { model: 'second', display_name: 'Second candidate', family_name: 'Family B', probability: 0.2 },
        { model: 'third', display_name: 'Third candidate', family_name: 'Family C', probability: 0.05 },
        { model: 'fourth', display_name: 'Hidden fourth candidate', family_name: 'Family D', probability: 0.025 }
      ],
      disclaimer: 'Closed-set statistical attribution.'
    }
    const wrapper = mount(ModelTraceResultPanel, { props: { result }, global: { stubs: { Icon: true } } })
    expect(wrapper.text()).toContain(`admin.accounts.modelTrace.${verdict}`)
    expect(wrapper.text()).toContain('72.5%')
    expect(wrapper.text()).toContain('Second candidate')
    expect(wrapper.text()).toContain('Third candidate')
    expect(wrapper.text()).not.toContain('Hidden fourth candidate')
    expect(wrapper.text()).toContain('admin.accounts.modelTrace.disclaimer')
  })
})
