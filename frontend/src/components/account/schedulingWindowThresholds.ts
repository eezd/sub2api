// 账号级按窗口停调阈值（5h 会话窗口 / 7d 周窗口）的共享逻辑。
// 仅 Anthropic OAuth / Setup Token 账号会收到订阅用量响应头，因此只对这两类账号开放；
// 后端同样只对这两类账号生效。值为 1-100，留空表示沿用统一阈值 / 平台设置。

export const SCHEDULING_WINDOW_THRESHOLD_KEYS = {
  '5h': 'account_scheduling_threshold_5h',
  '7d': 'account_scheduling_threshold_7d'
} as const

export type SchedulingWindowThresholdInput = string | number | null | undefined

export interface SchedulingWindowThresholdValues {
  threshold5h: SchedulingWindowThresholdInput
  threshold7d: SchedulingWindowThresholdInput
}

export function supportsSchedulingWindowThresholds(
  platform: string | undefined | null,
  type: string | undefined | null
): boolean {
  return platform === 'anthropic' && (type === 'oauth' || type === 'setup-token')
}

// 解析已存储 / 输入的阈值：非法或超出 1-100 视为未设置
export function normalizeSchedulingWindowThreshold(value: unknown): number | null {
  if (value === null || value === undefined || value === '') {
    return null
  }
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) {
    return null
  }
  const integer = Math.trunc(numeric)
  return integer >= 1 && integer <= 100 ? integer : null
}

function isEmptyInput(value: SchedulingWindowThresholdInput): boolean {
  return value === '' || value === null || value === undefined
}

// 输入值钳制到 1-100；空值返回 null
export function clampSchedulingWindowThreshold(value: SchedulingWindowThresholdInput): number | null {
  if (isEmptyInput(value)) {
    return null
  }
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) {
    return null
  }
  return Math.min(100, Math.max(1, Math.trunc(numeric)))
}

export function loadSchedulingWindowThresholds(
  credentials: Record<string, unknown> | undefined | null
): { threshold5h: number | ''; threshold7d: number | '' } {
  return {
    threshold5h: normalizeSchedulingWindowThreshold(credentials?.[SCHEDULING_WINDOW_THRESHOLD_KEYS['5h']]) ?? '',
    threshold7d: normalizeSchedulingWindowThreshold(credentials?.[SCHEDULING_WINDOW_THRESHOLD_KEYS['7d']]) ?? ''
  }
}

function entriesOf(values: SchedulingWindowThresholdValues): Array<[string, SchedulingWindowThresholdInput]> {
  return [
    [SCHEDULING_WINDOW_THRESHOLD_KEYS['5h'], values.threshold5h],
    [SCHEDULING_WINDOW_THRESHOLD_KEYS['7d'], values.threshold7d]
  ]
}

// 新建账号：只写入已填写的窗口
export function applySchedulingWindowThresholdsForCreate(
  credentials: Record<string, unknown>,
  values: SchedulingWindowThresholdValues
): void {
  for (const [key, raw] of entriesOf(values)) {
    const next = clampSchedulingWindowThreshold(raw)
    if (next !== null) {
      credentials[key] = next
    }
  }
}

// 编辑 / 批量编辑：留空写 null（清除覆盖），有值写钳制后的值。
// 传入 currentCredentials 时，与当前值相同的窗口不产生补丁。返回是否写入了任何键。
export function applySchedulingWindowThresholdsPatch(
  credentials: Record<string, unknown>,
  values: SchedulingWindowThresholdValues,
  currentCredentials?: Record<string, unknown> | null
): boolean {
  let changed = false
  for (const [key, raw] of entriesOf(values)) {
    const next = clampSchedulingWindowThreshold(raw)
    if (currentCredentials) {
      const current = normalizeSchedulingWindowThreshold(currentCredentials[key])
      if (current === next) {
        continue
      }
    }
    credentials[key] = next
    changed = true
  }
  return changed
}
