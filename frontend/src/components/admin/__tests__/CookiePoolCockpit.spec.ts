import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CookiePoolCockpit from '../CookiePoolCockpit.vue'
import type { CookieDashboard } from '@/api/admin/cookies'

describe('low Host cockpit', () => {
  it('includes forced accounts in the same group totals and opens matching account details', async () => {
    const dashboard = { pool_groups: [{
      policy: { group_id: 2, low_host_rotation_enabled: true, low_host_threshold: 5, low_host_account_slots: 10 },
      name: 'Normal', enabled: true, ready_capacity: 140, target_capacity: 100, active: 4, preparing: 1,
      low_host: { slots_used: 3, active: 2, preparing: 1, waiting: 5, blocked: 1, retiring: 0, ready_capacity: 70, degraded_continuing: 2 },
    }] } as CookieDashboard
    const wrapper = mount(CookiePoolCockpit, {
      props: { dashboard },
      global: { stubs: { CookiePoolAccountDialog: { props: ['initialState'], template: '<div data-testid="detail">{{ initialState }}</div>' } } },
    })
    expect(wrapper.text()).toContain('140 / 100 · 已覆盖')
    expect(wrapper.text()).toContain('本组总数已包含强制轮转：活跃 2 个、准备中 1 个、已就绪 70 并发')
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('button[aria-label="Normal 活跃（含到期退出） 查看账户"]').text()).toContain('4')
    const panel = wrapper.get('[data-testid="low-host-dashboard"]')
    expect(panel.text()).toContain('当前占用 3')
    expect(panel.text()).toContain('其中强制轮转就绪 70')
    await panel.get('button[aria-label="Normal 等待处理 查看账户"]').trigger('click')
    expect(wrapper.get('[data-testid="detail"]').text()).toBe('low_waiting')
    expect(wrapper.emitted('detailOpen')?.[0]).toEqual([true])
    wrapper.unmount()
  })
})
