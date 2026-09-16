import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent } from 'vue'
import IntelligentTestsView from '../IntelligentTestsView.vue'
import { intelligentTestsAPI } from '@/api/intelligentTests'

vi.mock('@/api/intelligentTests', () => ({ intelligentTestsAPI: { accounts: vi.fn(), settings: vi.fn(), records: vi.fn() }, newTestRequestKey: () => 'key' }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn().mockResolvedValue([]) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }), useRoute: () => ({ query: {} }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: defineComponent({ template: '<main><slot /></main>' }) }))
vi.mock('@/components/admin/intelligent-tests/AccountManagementTabs.vue', () => ({ default: defineComponent({ template: '<nav />' }) }))
const page = { items: [], total: 0, page: 1, page_size: 12, overview: { total_accounts: 0, tested_today: 0, success_accounts: 0, abnormal_accounts: 0, suspected_degradation: 0, review_accounts: 0 } }
const global = { stubs: { TestDetailDialog: true, TestResultCard: true, Pagination: true, RouterLink: true, Icon: true } }
beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); vi.mocked(intelligentTestsAPI.settings).mockResolvedValue([]) })
afterEach(() => { vi.useRealTimers() })

describe('intelligent test polling', () => {
  it('allows a quiet request slower than the poll interval to finish without aborting it', async () => {
    let finish!: (value: typeof page) => void
    vi.mocked(intelligentTestsAPI.accounts).mockResolvedValueOnce(page).mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(page)
    const wrapper = mount(IntelligentTestsView, { props: { mode: 'tests' }, global }); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    const signal = vi.mocked(intelligentTestsAPI.accounts).mock.calls[1][1]
    await vi.advanceTimersByTimeAsync(15000)
    expect(intelligentTestsAPI.accounts).toHaveBeenCalledTimes(2)
    expect(signal?.aborted).toBe(false)
    finish({ ...page, overview: { ...page.overview, review_accounts: 9 } }); await flushPromises()
    expect(wrapper.text()).toContain('答案待复核9')
    await vi.advanceTimersByTimeAsync(5000)
    expect(intelligentTestsAPI.accounts).toHaveBeenCalledTimes(3)
    wrapper.unmount()
  })
  it('keeps metadata errors until manual refresh successfully reloads settings', async () => {
    vi.mocked(intelligentTestsAPI.accounts).mockResolvedValue(page)
    vi.mocked(intelligentTestsAPI.settings).mockRejectedValueOnce({ message: '测试设置暂不可用' }).mockResolvedValue([])
    const wrapper = mount(IntelligentTestsView, { props: { mode: 'tests' }, global }); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(wrapper.text()).toContain('测试设置暂不可用')
    await wrapper.findAll('button').find(button => button.text() === '刷新')!.trigger('click'); await flushPromises()
    expect(intelligentTestsAPI.settings).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).not.toContain('测试设置暂不可用')
    wrapper.unmount()
  })
})
