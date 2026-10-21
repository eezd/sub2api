import { useAuthStore } from '@/stores/auth'

interface ModelSelectionPreference {
  keyID: number | null
  model: string
  protocol?: 'images' | 'gemini'
}

export function useModelSelectionPreference(scope: 'chat' | 'image-workbench', onError: (error: unknown) => void) {
  const auth = useAuthStore()
  const userID = auth.user?.id
  const storageKey = `sub2api:${scope}:selection:${userID}`

  function load(): ModelSelectionPreference | null {
    if (!userID) return null
    try {
      const raw = localStorage.getItem(storageKey)
      if (!raw) return null
      const value: unknown = JSON.parse(raw)
      if (!value || typeof value !== 'object' || !('keyID' in value) || !('model' in value)) {
        throw new Error('保存的密钥和模型选择无效，请重新选择')
      }
      const { keyID, model } = value
      const protocol = 'protocol' in value ? value.protocol : undefined
      if (!(keyID === null || (typeof keyID === 'number' && Number.isSafeInteger(keyID) && keyID > 0))
        || typeof model !== 'string' || (protocol !== undefined && protocol !== 'images' && protocol !== 'gemini')) {
        throw new Error('保存的密钥和模型选择无效，请重新选择')
      }
      return { keyID, model, protocol }
    } catch (error) { onError(error); return null }
  }

  function save(keyID: number | null, model: string, protocol?: 'images' | 'gemini') {
    // 只保存选择标识，并避免退出或切换账号时写入其他用户的偏好。
    if (!userID || auth.user?.id !== userID) return
    try { localStorage.setItem(storageKey, JSON.stringify({ keyID, model, ...(protocol ? { protocol } : {}) })) }
    catch (error) { onError(error) }
  }

  return { load, save }
}
