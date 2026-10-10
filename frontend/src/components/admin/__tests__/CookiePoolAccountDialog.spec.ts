import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import CookiePoolAccountDialog from '../CookiePoolAccountDialog.vue'
import { getDashboard, type CookieDashboard } from '@/api/admin/cookies'

vi.mock('@/api/admin/cookies', () => ({ getDashboard: vi.fn() }))

const response = {
  pool_groups: [{
    policy: { group_id: 2 },
    accounts: [{
      id: 1, name: 'account', state: 'preparing', concurrency: 35,
      available_hosts: 1,
      hosts: [{ host: 'available.example', state: 'available', expires_at: '2099-01-01T00:00:00Z' }]
    }]
  }]
} as CookieDashboard

function render() {
  return mount(CookiePoolAccountDialog, {
    props: { groupId: 2, groupName: 'Normal', initialState: 'preparing' },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
  })
}

describe('Cookie pool account loading', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(getDashboard).mockReset()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  })
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

  it('renders Host rows only while expanded', async () => {
    vi.mocked(getDashboard).mockResolvedValue(response)
    const wrapper = render()
    try {
      await flushPromises()
      expect(wrapper.text()).not.toContain('available.example')
      const details = wrapper.get('details')
      ;(details.element as HTMLDetailsElement).open = true
      await details.trigger('toggle')
      expect(wrapper.text()).toContain('available.example')
      const search = wrapper.get('input[type="search"]')
      await search.setValue('no matching account')
      expect(wrapper.find('details').exists()).toBe(false)
      await search.setValue('')
      const restored = wrapper.get('details')
      expect((restored.element as HTMLDetailsElement).open).toBe(true)
      ;(restored.element as HTMLDetailsElement).open = false
      await restored.trigger('toggle')
      expect(wrapper.text()).not.toContain('available.example')
    } finally { wrapper.unmount() }
  })

  it('does not overlap slow requests and backs off after a failure', async () => {
    let reject!: (error: Error) => void
    vi.mocked(getDashboard).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
    vi.mocked(getDashboard).mockResolvedValue(response)
    const wrapper = render()
    try {
      await vi.advanceTimersByTimeAsync(20000)
      expect(getDashboard).toHaveBeenCalledTimes(1)
      reject(new Error('timeout'))
      await flushPromises()
      await vi.advanceTimersByTimeAsync(9000)
      expect(getDashboard).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1000)
      expect(getDashboard).toHaveBeenCalledTimes(2)
    } finally { wrapper.unmount() }
    await vi.advanceTimersByTimeAsync(30000)
    expect(getDashboard).toHaveBeenCalledTimes(2)
  })
})
