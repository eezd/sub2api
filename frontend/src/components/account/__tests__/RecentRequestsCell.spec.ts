import { afterEach, describe, expect, it, vi } from 'vitest'
import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import RecentRequestsCell from '../RecentRequestsCell.vue'
import type { OpsRequestDetail } from '@/api/admin/ops'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const request = (overrides: Partial<OpsRequestDetail> = {}): OpsRequestDetail => ({
  kind: 'success',
  created_at: '2026-09-19T08:00:00Z',
  request_id: 'req-default',
  model: 'gpt-5.6',
  duration_ms: 8320,
  first_token_ms: 514,
  input_tokens: 1024,
  output_tokens: 386,
  actual_cost: 0.012345,
  account_cost: 0.010617,
  account_rate_multiplier: 0.86,
  ...overrides
})

const mountCell = (props: Partial<InstanceType<typeof RecentRequestsCell>['$props']> = {}) => mount(
  RecentRequestsCell,
  {
    attachTo: document.body,
    props: {
      accountId: 12,
      accountName: 'timeline-demo',
      requests: [],
      ...props
    }
  }
)

afterEach(() => {
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('RecentRequestsCell', () => {
  it('renders at most five requests from oldest to newest with readable success and error labels', () => {
    const requests = Array.from({ length: 6 }, (_, index) => request({
      kind: index === 1 ? 'error' : 'success',
      request_id: `req-${index}`,
      error_id: index === 1 ? 91 : undefined,
      created_at: `2026-09-19T08:0${index}:00Z`
    })).reverse()
    const wrapper = mountCell({ requests })

    const buttons = wrapper.findAll('[data-testid="recent-request-bars"] button')
    expect(buttons).toHaveLength(5)
    expect(buttons[0].attributes('aria-label')).toContain('admin.accounts.recentRequests.error')
    expect(buttons[4].attributes('aria-label')).toContain('admin.accounts.recentRequests.success')
    expect(buttons[0].get('span').classes()).toContain('bg-red-500')
    expect(buttons[4].get('span').classes()).toContain('bg-emerald-500')
    wrapper.unmount()
  })

  it('prioritizes an existing stale snapshot over loading and error states', () => {
    const stale = mountCell({ requests: [request()], loading: true, loadError: true, stale: true })
    expect(stale.findAll('[data-testid="recent-request-bars"] button')).toHaveLength(1)
    expect(stale.find('[data-testid="recent-requests-stale"]').exists()).toBe(true)
    expect(stale.text()).not.toContain('admin.accounts.recentRequests.loadFailed')
    stale.unmount()

    const loading = mountCell({ loading: true, loadError: true })
    expect(loading.find('[aria-busy="true"]').exists()).toBe(true)
    expect(loading.text()).not.toContain('admin.accounts.recentRequests.loadFailed')
    loading.unmount()

    const failed = mountCell({ loadError: true })
    expect(failed.text()).toContain('admin.accounts.recentRequests.loadFailed')
    failed.unmount()

    const empty = mountCell()
    expect(empty.text()).toContain('—')
    empty.unmount()
  })

  it('opens details on hover and preserves zero values while escaping error text', async () => {
    const wrapper = mountCell({ requests: [request({
      kind: 'error',
      error_id: 7,
      status_code: 429,
      message: '<img src=x onerror=alert(1)>',
      input_tokens: 0,
      output_tokens: 0,
      actual_cost: 0,
      account_cost: null,
      account_rate_multiplier: null
    })] })

    await wrapper.get('[data-testid="recent-request-bars"] button').trigger('mouseenter')
    await flushPromises()

    const dialogElement = document.body.querySelector('[role="dialog"]')
    expect(dialogElement).not.toBeNull()
    const dialog = new DOMWrapper(dialogElement!)
    expect(dialog.text()).toContain('timeline-demo')
    expect(dialog.text()).toContain('#12')
    expect(dialog.text()).toContain('$0.000000')
    expect(dialog.text()).toContain('<img src=x onerror=alert(1)>')
    expect(dialog.find('img').exists()).toBe(false)
    expect(dialog.text()).toContain('429')
    wrapper.unmount()
  })

  it('closes a hovered request on Escape', async () => {
    const wrapper = mountCell({ requests: [request()] })
    const trigger = wrapper.get('[data-testid="recent-request-bars"] button')
    trigger.element.focus()
    await trigger.trigger('mouseenter')
    await flushPromises()
    await trigger.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })

  it('pins on click, focuses close, and restores trigger focus when closed', async () => {
    const wrapper = mountCell({ requests: [request()] })
    const trigger = wrapper.get('[data-testid="recent-request-bars"] button')
    await trigger.trigger('click')
    await flushPromises()
    const dialog = new DOMWrapper(document.body.querySelector('[role="dialog"]')!)
    const close = dialog.get('button')
    expect(document.activeElement).toBe(close.element)
    ;(close.element as HTMLButtonElement).click()
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(trigger.element)
    wrapper.unmount()
  })

  it('keeps a pinned request stable when polling shifts its bar position', async () => {
    const pinned = request({ request_id: 'req-pinned', created_at: '2026-09-19T08:02:00Z' })
    const wrapper = mountCell({
      requests: [
        pinned,
        request({ request_id: 'req-middle', created_at: '2026-09-19T08:01:00Z' }),
        request({ request_id: 'req-old', created_at: '2026-09-19T08:00:00Z' })
      ]
    })
    await wrapper.findAll('[data-testid="recent-request-bars"] button')[2].trigger('click')
    await flushPromises()

    await wrapper.setProps({
      requests: [
        request({ request_id: 'req-new', created_at: '2026-09-19T08:03:00Z' }),
        pinned,
        request({ request_id: 'req-middle', created_at: '2026-09-19T08:01:00Z' })
      ]
    })
    await flushPromises()
    await wrapper.findAll('[data-testid="recent-request-bars"] button')[2].trigger('mouseenter')
    await flushPromises()

    const dialog = new DOMWrapper(document.body.querySelector('[role="dialog"]')!)
    expect(dialog.text()).toContain('req-pinned')
    expect(dialog.text()).not.toContain('req-new')
    wrapper.unmount()
  })

  it('closes a pinned request after it leaves the refreshed top five', async () => {
    const wrapper = mountCell({ requests: [request({ request_id: 'req-dropped' })] })
    await wrapper.get('[data-testid="recent-request-bars"] button').trigger('click')
    await flushPromises()

    await wrapper.setProps({ requests: [request({ request_id: 'req-replacement' })] })
    await flushPromises()

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })
})
