import { mount, flushPromises } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import GroupStatisticsDialog from '../GroupStatisticsDialog.vue'
import { getStats, type GroupDetailStats } from '@/api/admin/groups'

vi.mock('@/api/admin/groups', () => ({ getStats: vi.fn() }))
const data: GroupDetailStats = { group_id: 1, group_name: 'First group', total_api_keys: 3, active_api_keys: 1, total_accounts: 2, total_requests: 3, total_tokens: 49, total_cost: 32, total_actual_cost: 16, total_account_cost: 13, balance_cost: 12, subscription_cost: 4, zero_charge_requests: 1, average_duration_ms: 2000, from: null, to: null, generated_at: '2026-09-14T12:00:00Z' }
const global = { stubs: { BaseDialog: defineComponent({ props: ['show', 'title'], template: '<div v-if="show"><h1>{{ title }}</h1><slot /><slot name="footer" /></div>' }) } }
beforeEach(() => { vi.clearAllMocks(); vi.mocked(getStats).mockResolvedValue(data) })

describe('optional group detail statistics', () => {
  it('does not request statistics while closed and keeps the three cost bases separate', async () => {
    const wrapper = mount(GroupStatisticsDialog, { props: { groupId: null }, global })
    expect(getStats).not.toHaveBeenCalled()
    await wrapper.setProps({ groupId: 1 }); await flushPromises()
    expect(wrapper.text()).toContain('实际计费用量'); expect(wrapper.text()).toContain('$16.00')
    expect(wrapper.text()).toContain('账面费用'); expect(wrapper.text()).toContain('$32.00')
    expect(wrapper.text()).toContain('上游账号成本'); expect(wrapper.text()).toContain('$13.00')
    expect(wrapper.text()).toContain('不是现金支付')
    wrapper.unmount()
  })
  it('does not let the previous group overwrite a newly selected group', async () => {
    let first!: (value: GroupDetailStats) => void
    vi.mocked(getStats).mockImplementationOnce(() => new Promise(resolve => { first = resolve })).mockResolvedValueOnce({ ...data, group_id: 2, group_name: 'Second group' })
    const wrapper = mount(GroupStatisticsDialog, { props: { groupId: 1 }, global })
    await wrapper.setProps({ groupId: 2 }); await flushPromises()
    first(data); await flushPromises()
    expect(wrapper.text()).toContain('Second group'); expect(wrapper.text()).not.toContain('First group')
    wrapper.unmount()
  })
})
