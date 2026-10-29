import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'

describe('translation HTML safety', () => {
  it('neutralizes iframe srcdoc while preserving ordinary interpolation', () => {
    const i18n = createI18n({
      legacy: false,
      locale: 'en',
      escapeParameter: true,
      warnHtmlMessage: false,
      messages: {
        en: {
          embedded: '<iframe srcdoc="<b>untrusted content</b>"></iframe>',
          greeting: 'Hello {name}'
        }
      }
    })
    const fragment = document.createElement('template')
    fragment.innerHTML = i18n.global.t('embedded')
    const iframe = fragment.content.querySelector('iframe')
    expect(iframe).not.toBeNull()
    expect(iframe!.hasAttribute('srcdoc')).toBe(false)
    expect(i18n.global.t('greeting', { name: 'Alice' })).toBe('Hello Alice')
  })
})
