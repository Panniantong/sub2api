import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import DashboardView from '../DashboardView.vue'

const { api, profile, quotas, stats } = vi.hoisted(() => {
  const stats = { total_api_keys: 0, today_requests: 12, today_actual_cost: 0.125 }
  return { stats, profile: vi.fn(), quotas: vi.fn(), api: { getDashboardStats: vi.fn(), getDashboardTrend: vi.fn(), getDashboardModels: vi.fn(), getByDateRange: vi.fn() } }
})
vi.mock('@/api/usage', () => ({ usageAPI: api }))
vi.mock('@/api/user', () => ({ getMyPlatformQuotas: quotas }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { username: '真实用户', balance: 25 }, isSimpleMode: false, refreshUser: profile }) }))
vi.mock('@/composables/useRelayBrand', () => ({ useRelayBrand: () => ({ siteName: '澄川 AI' }) }))
vi.mock('@/composables/useRelayEndpoint', () => ({ useRelayEndpoint: () => ({ endpoint: 'https://relay.example/v1' }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copied: false, copyToClipboard: vi.fn().mockResolvedValue(true) }) }))

function mountDashboard() {
  return mount(DashboardView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    RouterLink: RouterLinkStub, Icon: true, LoadingSpinner: true, RelayQuickStart: true, UserDashboardQuickActions: true,
    UserDashboardStats: { props: ['stats', 'balance'], template: '<div data-testid="stats">{{ stats.today_requests }} / {{ stats.today_actual_cost }} / {{ balance }}</div>' },
    UserDashboardCharts: { props: ['trend', 'models'], emits: ['refresh', 'dateRangeChange'], template: '<div data-testid="charts">{{ trend }} {{ models }}</div>' },
    UserDashboardRecentUsage: { props: ['data'], template: '<div data-testid="recent">{{ data }}</div>' },
  } } })
}

describe('relay dashboard loading and recovery', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.getDashboardStats.mockResolvedValue(stats)
    api.getDashboardTrend.mockResolvedValue({ trend: [] })
    api.getDashboardModels.mockResolvedValue({ models: [] })
    api.getByDateRange.mockResolvedValue({ items: [] })
    profile.mockResolvedValue(undefined)
    quotas.mockResolvedValue({ platform_quotas: [] })
  })

  it('renders server stats and first-key onboarding without synthetic counts', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toBe('12 / 0.125 / 25')
    expect(wrapper.text()).toContain('创建第一把 API 密钥')
    expect(wrapper.text()).toContain('https://relay.example/v1')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('keeps quick start reachable and recovers when stats fail then retry succeeds', async () => {
    api.getDashboardStats.mockRejectedValueOnce(new Error('network unavailable'))
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('账户统计加载失败')
    expect(wrapper.find('relay-quick-start-stub').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stats"]').exists()).toBe(false)
    const refresh = wrapper.findAll('button').find(button => button.text() === '刷新数据')!
    await refresh.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('12')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('does not discard successful statistics when an independent balance request fails', async () => {
    profile.mockRejectedValue(new Error('unavailable'))
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('12')
    expect(wrapper.get('[role="alert"]').text()).toContain('余额与账户信息更新失败')
  })
})
