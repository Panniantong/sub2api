import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import Dashboard from '../PelicanRecordsDashboard.vue'
import { scheduledTestsAPI as api } from '@/api/admin/scheduledTests'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/scheduledTests', () => ({ scheduledTestsAPI: { listPelicanHistory: vi.fn(), getResult: vi.fn() } }))
const account = { id: 42, name: 'Manual account' } as any
const manual = { createdAt: '2026-09-23T12:00:00Z', modelId: 'saved-model', reasoningEffort: 'medium', runs: [{ html: '<html><body>MANUAL-ANIMATION</body></html>', output: '', durationMs: 34000, status: 'success' }] }
function render(extra = {}) { return mount(Dashboard, { props: { accounts: [], account, manualRecord: manual, ...extra } }) }
beforeEach(() => { vi.useFakeTimers(); vi.resetAllMocks(); localStorage.clear(); vi.mocked(api.listPelicanHistory).mockResolvedValue({ items: [], next_cursor: 0 }) })
afterEach(() => { vi.useRealTimers() })
describe('Pelican record dashboard', () => {
  it('shows and opens a manual result when the current account is outside the list', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('[data-testid="pelican-record-card"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('saved-model / medium')
    expect(wrapper.text()).toContain('34.0 s')
    expect(wrapper.text()).toContain('sourceManual')
    await wrapper.get('article button').trigger('click')
    expect(wrapper.get('[data-testid="record-detail"] iframe').attributes('srcdoc')).toContain('MANUAL-ANIMATION')
    expect(wrapper.get('[data-testid="record-detail"] iframe').attributes('srcdoc')).toContain('Content-Security-Policy')
    expect(wrapper.get('article iframe').classes()).toContain('pointer-events-none')
    wrapper.unmount()
  })
  it('keeps local records if the server API fails and omits empty accounts', async () => {
    vi.mocked(api.listPelicanHistory).mockRejectedValue(new Error('offline'))
    const wrapper = render({ accounts: [{ id: 99, name: 'Empty account' }] }); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('Empty account')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('loads other accounts manual history and never fabricates duration or time', async () => {
    localStorage.setItem('sub2api-pelican-test:99', JSON.stringify([{ ...manual, createdAt: '', runs: [{ html: '<svg></svg>' }] }]))
    const wrapper = render({ account: null, manualRecord: null, accounts: [{ id: 99, name: 'Other manual account' }] }); await flushPromises()
    expect(wrapper.text()).toContain('Other manual account')
    expect(wrapper.text()).not.toContain('0.0 s')
    expect(wrapper.text()).toContain('—')
    wrapper.unmount()
  })

  it('shows all six outputs from one account including failures, plus another account and manual runs', async () => {
    const results = Array.from({ length: 7 }, (_, i) => ({ id: i + 1, plan_id: i < 6 ? 2 : 5, account_id: i < 6 ? 324 : 339, account_name: i < 6 ? 'Account 324' : 'Account 339', status: i === 5 ? 'failed' : 'success', error_message: i === 5 ? 'stream interrupted' : '', response_text: '', started_at: `2026-09-23T13:00:0${i}Z`, latency_ms: 1000, pelican_config: { model_id: 'gpt-6-astra', reasoning_effort: 'medium' } }))
    vi.mocked(api.listPelicanHistory).mockImplementation(async cursor => cursor === 2 ? { items: results.slice(2) as any, next_cursor: 0 } : { items: results.slice(0, 2) as any, next_cursor: 2 })
    vi.mocked(api.getResult).mockImplementation(async (_, id) => ({ ...results.find(r => r.id === id), response_text: `<svg>RECORD-${id}</svg>` }) as any)
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(8)
    expect(wrapper.findAll('article').filter(c => c.text().includes('Account 324'))).toHaveLength(6)
    expect(wrapper.text()).toContain('Account 339')
    expect(wrapper.text()).toContain('stream interrupted')
    expect(api.listPelicanHistory).toHaveBeenCalledWith(2)
    const failed = wrapper.findAll('article').find(c => c.text().includes('stream interrupted'))!
    await failed.get('button').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="record-detail"] iframe').attributes('srcdoc')).toContain('RECORD-6')
    expect(wrapper.get('[data-testid="record-detail"]').text()).toContain('sourceScheduled')
    wrapper.unmount()
  })

  it('keeps every parallel manual output, deduplicates saved/current history, and reads other pages', async () => {
    const batch = { ...manual, id: 'batch', runs: [manual.runs[0], { ...manual.runs[0], html: '<svg>SECOND</svg>' }] }
    localStorage.setItem('sub2api-pelican-test:42', JSON.stringify([batch]))
    localStorage.setItem('sub2api-pelican-test:999', JSON.stringify([manual]))
    const wrapper = render({ manualRecord: batch }); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(3)
    expect(wrapper.text()).toContain('#999')
    wrapper.unmount()
  })

  it('refreshes completed results and stops polling when closed', async () => {
    const wrapper = render({ manualRecord: null }); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(0)
    const row = { id: 8, plan_id: 3, account_id: 400, account_name: 'New account', started_at: '2026-09-23T14:00:00Z', latency_ms: 2000, status: 'success', response_text: '' }
    vi.mocked(api.listPelicanHistory).mockResolvedValue({ items: [row] as any, next_cursor: 0 })
    vi.mocked(api.getResult).mockResolvedValue({ ...row, response_text: '<svg>new result</svg>' } as any)
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.text()).toContain('New account')
    wrapper.unmount()
    const calls = vi.mocked(api.listPelicanHistory).mock.calls.length
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.listPelicanHistory).toHaveBeenCalledTimes(calls)
  })
})
