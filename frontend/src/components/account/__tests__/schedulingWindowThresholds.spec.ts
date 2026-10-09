import { describe, expect, it } from 'vitest'
import {
  applySchedulingWindowThresholdsForCreate,
  applySchedulingWindowThresholdsPatch,
  loadSchedulingWindowThresholds,
  supportsSchedulingWindowThresholds
} from '../schedulingWindowThresholds'

describe('schedulingWindowThresholds', () => {
  it('仅 Anthropic OAuth / Setup Token 支持按窗口阈值', () => {
    expect(supportsSchedulingWindowThresholds('anthropic', 'oauth')).toBe(true)
    expect(supportsSchedulingWindowThresholds('anthropic', 'setup-token')).toBe(true)
    expect(supportsSchedulingWindowThresholds('anthropic', 'apikey')).toBe(false)
    expect(supportsSchedulingWindowThresholds('anthropic', 'bedrock')).toBe(false)
    expect(supportsSchedulingWindowThresholds('openai', 'oauth')).toBe(false)
  })

  it('加载时忽略非法值', () => {
    expect(
      loadSchedulingWindowThresholds({
        account_scheduling_threshold_5h: '100',
        account_scheduling_threshold_7d: 0
      })
    ).toEqual({ threshold5h: 100, threshold7d: '' })
    expect(loadSchedulingWindowThresholds(undefined)).toEqual({ threshold5h: '', threshold7d: '' })
  })

  it('新建只写入已填写的窗口并钳制到 1-100', () => {
    const credentials: Record<string, unknown> = {}
    applySchedulingWindowThresholdsForCreate(credentials, { threshold5h: '', threshold7d: 150 })
    expect(credentials).toEqual({ account_scheduling_threshold_7d: 100 })
  })

  it('编辑补丁：未变化不写，留空写 null', () => {
    const credentials: Record<string, unknown> = {}
    const changed = applySchedulingWindowThresholdsPatch(
      credentials,
      { threshold5h: '', threshold7d: 80 },
      { account_scheduling_threshold_5h: 100, account_scheduling_threshold_7d: 80 }
    )
    expect(changed).toBe(true)
    expect(credentials).toEqual({ account_scheduling_threshold_5h: null })

    const untouched: Record<string, unknown> = {}
    expect(applySchedulingWindowThresholdsPatch(untouched, { threshold5h: '', threshold7d: '' }, {})).toBe(false)
    expect(untouched).toEqual({})
  })
})
