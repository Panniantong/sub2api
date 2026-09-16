import { mount, flushPromises } from '@vue/test-utils'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent } from 'vue'
import { testImageURL } from '../display'
import QuickTestDialog from '../QuickTestDialog.vue'
import TestSettingsPanel from '../TestSettingsPanel.vue'
import TestResultCard from '../TestResultCard.vue'
import TestDetailDialog from '../TestDetailDialog.vue'
import TestGeneratedImage from '../TestGeneratedImage.vue'
import UserAccountCapabilities from '@/components/account/UserAccountCapabilities.vue'
import { intelligentTestsAPI, newTestRequestKey, type TestSetting, type TestRecord, type TestAccount } from '@/api/intelligentTests'
import { getAvailableModels } from '@/api/admin/accounts'

vi.mock('@/api/intelligentTests', () => ({
  intelligentTestsAPI: { settings: vi.fn(), run: vi.fn(), saveSetting: vi.fn(), detail: vi.fn(), publicDetail: vi.fn(), publicAccounts: vi.fn(), publicTests: vi.fn(), image: vi.fn(), reevaluate: vi.fn(), cancel: vi.fn(), previewEvaluation: vi.fn() },
  newTestRequestKey: vi.fn(() => 'fixed-request-key')
}))
vi.mock('@/api/admin/accounts', () => ({ getAvailableModels: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: vi.fn() }) }))
const BaseDialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' })
const settings: TestSetting[] = [{ test_type: 'pelican', enabled: true, user_visible: false, config: { prompt: 'Draw a pelican', model: '', evaluator: 'svg_structure', timeout_seconds: 180 } }, { test_type: 'candy', enabled: false, user_visible: true, config: { prompt: '12 minus 5?', model: '', evaluator: 'exact_answer', expected_answer: '7', timeout_seconds: 120 } }]
const record: TestRecord = { id: 1, account_id: 42, test_type: 'pelican', status: 'running', score: null, result: '', result_image: '', duration_ms: 0, model: 'test-model', anti_degradation: true, started_at: '2026-09-12T12:00:00Z', finished_at: null, created_at: '2026-09-12T12:00:00Z' }
const global = { stubs: { BaseDialog, Pagination: true, TestStatusBadge: true } }
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(intelligentTestsAPI.settings).mockResolvedValue(structuredClone(settings))
  vi.mocked(getAvailableModels).mockResolvedValue([{ id: 'gpt-5.3-codex', display_name: 'GPT-5.3 Codex' }, { id: 'gpt-5.5' }])
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })

describe('model-generated image isolation', () => {
  it('loads a historical image with authorization, ignores stale results and revokes object URLs', async () => {
    let oldImage!: (value: Blob) => void
    const svg = '<svg viewBox="0 0 600 420"><circle r="10"/></svg>'
    const blob = { type: 'image/svg+xml', size: svg.length, text: async () => svg } as Blob
    const createObjectURL = vi.fn(() => 'blob:fixture-image'), revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', class extends URL { static createObjectURL = createObjectURL; static revokeObjectURL = revokeObjectURL })
    vi.mocked(intelligentTestsAPI.image).mockImplementationOnce(() => new Promise(resolve => { oldImage = resolve })).mockResolvedValueOnce(blob)
    const wrapper = mount(TestGeneratedImage, { props: { recordId: 1, publicView: true, fullSize: true } })
    const signal = vi.mocked(intelligentTestsAPI.image).mock.calls[0][2]
    await wrapper.setProps({ recordId: 2 }); await flushPromises()
    expect(signal?.aborted).toBe(true)
    expect(intelligentTestsAPI.image).toHaveBeenNthCalledWith(2, 2, true, expect.any(AbortSignal))
    expect(wrapper.get('img').attributes('src')).toBe('blob:fixture-image')
    expect(wrapper.get('img').attributes('style')).toContain('width: 600px')
    oldImage(blob); await flushPromises()
    expect(createObjectURL).toHaveBeenCalledTimes(1)
    wrapper.unmount()
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:fixture-image')
  })
  it('rejects an image endpoint response with an unexpected content type', async () => {
    vi.mocked(intelligentTestsAPI.image).mockResolvedValue({ type: 'text/html', size: 20 } as Blob)
    const wrapper = mount(TestGeneratedImage, { props: { recordId: 3 } }); await flushPromises()
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('暂无可安全显示的图像')
    wrapper.unmount()
  })
  it('renders shapes but removes executable and external content', () => {
    const uri = testImageURL('<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><foreignObject><div>unsafe</div></foreignObject><image href="https://example.invalid/track"/><use href="https://example.invalid/a.svg#x"/><circle cx="10" cy="10" r="5"/></svg>')
    const svg = decodeURIComponent(uri.split(',')[1])
    expect(svg).toContain('<circle')
    expect(svg).not.toMatch(/script|foreignObject|onload|https:\/\/example|<image|<use/i)
  })
  it('rejects arbitrary URLs, HTML and oversized output', () => {
    expect(testImageURL('javascript:alert(1)')).toBe('')
    expect(testImageURL('https://example.invalid/image.png')).toBe('')
    expect(testImageURL('<img src=x onerror=alert(1)>')).toBe('')
    expect(testImageURL('<svg>' + 'a'.repeat(2_000_000))).toBe('')
  })
})

describe('asynchronous quick tests', () => {
  it('changes the retry key when the selected model changes and filters media models', async () => {
    vi.mocked(newTestRequestKey).mockReturnValueOnce('request-for-model-1').mockReturnValueOnce('request-for-model-2')
    vi.mocked(getAvailableModels).mockResolvedValue([{ id: 'gpt-image-1' }, { id: 'gpt-5.4' }, { id: 'gpt-5.4-mini' }])
    vi.mocked(intelligentTestsAPI.run).mockRejectedValue(new Error('network'))
    const wrapper = mount(QuickTestDialog, { props: { accountId: 42 }, global }); await flushPromises()
    expect(wrapper.find('select').text()).not.toContain('gpt-image-1')
    await wrapper.find('select').setValue('gpt-5.4')
    await wrapper.findAll('button')[0].trigger('click'); await flushPromises()
    await wrapper.find('select').setValue('gpt-5.4-mini')
    await wrapper.findAll('button')[0].trigger('click'); await flushPromises()
    const calls = vi.mocked(intelligentTestsAPI.run).mock.calls
    expect(calls[0][2]).not.toBe(calls[1][2])
    expect(calls[1][3]).toEqual({ pelican: 'gpt-5.4-mini' })
    wrapper.unmount()
  })
  it('only submits enabled types and prevents concurrent submission', async () => {
    let resolve: (value: { records: TestRecord[]; reused: boolean }) => void = () => undefined
    vi.mocked(intelligentTestsAPI.run).mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mount(QuickTestDialog, { props: { accountId: null }, global })
    await wrapper.setProps({ accountId: 42 }); await flushPromises()
    const buttons = wrapper.findAll('button')
    expect(buttons[1].attributes('disabled')).toBeDefined()
    await buttons[2].trigger('click'); await buttons[2].trigger('click')
    expect(intelligentTestsAPI.run).toHaveBeenCalledTimes(1)
    expect(intelligentTestsAPI.run).toHaveBeenCalledWith([42], ['pelican'], 'fixed-request-key')
    resolve({ records: [record], reused: false }); await flushPromises()
    expect(wrapper.emitted('queued')).toHaveLength(1)
    wrapper.unmount()
  })
  it('reuses the submission key after a network error', async () => {
    vi.mocked(intelligentTestsAPI.run).mockRejectedValueOnce(new Error('network')).mockResolvedValue({ records: [record], reused: true })
    const wrapper = mount(QuickTestDialog, { props: { accountId: null }, global })
    await wrapper.setProps({ accountId: 42 }); await flushPromises()
    await wrapper.findAll('button')[0].trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('network')
    await wrapper.findAll('button')[0].trigger('click'); await flushPromises()
    expect(vi.mocked(intelligentTestsAPI.run).mock.calls[0][2]).toBe(vi.mocked(intelligentTestsAPI.run).mock.calls[1][2])
    wrapper.unmount()
  })
  it('submits the administrator-selected model override', async () => {
    vi.mocked(intelligentTestsAPI.run).mockResolvedValue({ records: [record], reused: false })
    const wrapper = mount(QuickTestDialog, { props: { accountId: null }, global })
    await wrapper.setProps({ accountId: 42 }); await flushPromises()
    await wrapper.find('select').setValue('gpt-5.5')
    await wrapper.findAll('button')[0].trigger('click'); await flushPromises()
    expect(intelligentTestsAPI.run).toHaveBeenCalledWith([42], ['pelican'], 'fixed-request-key', { pelican: 'gpt-5.5' })
    wrapper.unmount()
  })
})

describe('independent settings and persisted status', () => {
  it('saves an explicit no-unit rule instead of silently inferring the candy unit', async () => {
    vi.mocked(intelligentTestsAPI.saveSetting).mockImplementation(async value => value)
    const wrapper = mount(TestSettingsPanel, { props: { settings: structuredClone(settings) } })
    const form = wrapper.findAll('form')[1]
    const units = form.findAll('select').find(select => select.text().includes('不接受单位'))!
    await units.setValue('none')
    await form.trigger('submit'); await flushPromises()
    expect(vi.mocked(intelligentTestsAPI.saveSetting).mock.calls[0][0].config.answer_unit_mode).toBe('none')
    wrapper.unmount()
  })
  it('can re-evaluate an earlier B record with the corrected version and preserve its judgment', async () => {
    vi.mocked(intelligentTestsAPI.detail).mockResolvedValue({ ...record, test_type: 'candy', status: 'completed', score: 100, result: '因此答案不是12', evaluation: { evaluator_version: 2, answer_verdict: 'correct' } })
    vi.mocked(intelligentTestsAPI.reevaluate).mockResolvedValue({ ...record, test_type: 'candy', status: 'completed', score: null, result: '因此答案不是12', evaluation: { evaluator_version: 3, answer_verdict: 'undetermined', format_verdict: 'non_compliant', original_judgment: { evaluator_version: 2, score: 100 } } })
    const wrapper = mount(TestDetailDialog, { props: { recordId: 1 }, global }); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('重新评估'))!.trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid=answer-verdict]').text()).toBe('答案需复核')
    expect(wrapper.text()).toContain('原判定（已保留）')
    expect(wrapper.findAll('button').some(button => button.text().includes('重新评估'))).toBe(false)
    expect(intelligentTestsAPI.run).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('does not let a late queued response overwrite a successful cancellation', async () => {
    vi.useFakeTimers()
    let stale!: (value: TestRecord) => void
    const queued: TestRecord = { ...record, test_type: 'candy', status: 'queued' }
    vi.mocked(intelligentTestsAPI.detail).mockResolvedValueOnce(queued).mockImplementationOnce(() => new Promise(resolve => { stale = resolve }))
    vi.mocked(intelligentTestsAPI.cancel).mockResolvedValue({ ...queued, status: 'cancelled' })
    const wrapper = mount(TestDetailDialog, { props: { recordId: 1 }, global }); await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await wrapper.findAll('button').find(button => button.text() === '取消排队')!.trigger('click'); await flushPromises()
    stale(queued); await flushPromises()
    expect(wrapper.findComponent({ name: 'TestStatusBadge' }).props('status')).toBe('cancelled')
    expect(wrapper.text()).not.toContain('取消排队')
    wrapper.unmount()
  })
  it('preserves a dirty settings draft when another setting is refreshed', async () => {
    const wrapper = mount(TestSettingsPanel, { props: { settings: structuredClone(settings) } })
    await wrapper.findAll('form')[1].find('textarea').setValue('My unsaved question')
    await wrapper.setProps({ settings: [{ ...settings[0], enabled: false }, structuredClone(settings[1])] })
    expect((wrapper.findAll('form')[1].find('textarea').element as HTMLTextAreaElement).value).toBe('My unsaved question')
    wrapper.unmount()
  })
  it('renders the generated pelican while a new attempt waits and keeps B verdicts distinct', () => {
    const account: TestAccount = { account_id: 42, account_type: 'oauth', account_status: 'active', name: 'A', platform: 'openai', group_ids: [], anti_degradation: true, tests: [] }
    const completed: TestRecord = { ...record, id: 7, status: 'completed', result_image: '<svg viewBox="0 0 20 20"><circle r="5" cx="10" cy="10"/></svg>', evaluation: { evaluator_version: 2, answer_verdict: 'not_evaluated', format_verdict: 'compliant' } }
    const wrapper = mount(TestResultCard, { props: { account, summary: { test_type: 'pelican', latest: { ...record, id: 8, status: 'queued' }, latest_completed: completed, history_count: 2, consecutive_anomalies: 0, risk: '' }, now: Date.now() }, global })
    expect(wrapper.find('img').attributes('src')).toContain('data:image/svg+xml')
    expect(wrapper.text()).toContain('最近完成的结果 · #7')
    expect(wrapper.text()).toContain('内容未自动评估')
    expect(wrapper.text()).not.toContain('100')
    expect(wrapper.text()).toContain('取消排队')
    wrapper.unmount()
  })
  it('shows a correct answer independently of a format difference and preserves original judgment', async () => {
    vi.mocked(intelligentTestsAPI.detail).mockResolvedValue({ ...record, test_type: 'candy', status: 'suspected_degradation', result: '12', config_snapshot: { expected_answer: '12' } })
    vi.mocked(intelligentTestsAPI.reevaluate).mockResolvedValue({ ...record, test_type: 'candy', status: 'completed', score: 100, result: '12', evaluation: { evaluator_version: 2, answer_verdict: 'correct', format_verdict: 'non_compliant', original_judgment: { status: 'suspected_degradation' } } })
    const wrapper = mount(TestDetailDialog, { props: { recordId: 1 }, global }); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('重新评估'))!.trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid=answer-verdict]').text()).toBe('最终答案正确')
    expect(wrapper.get('[data-testid=format-verdict]').text()).toBe('格式有差异')
    expect(wrapper.text()).toContain('原判定（已保留）')
    expect(intelligentTestsAPI.run).not.toHaveBeenCalled()
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })
  it('locks the submitted form and acknowledges only its submission snapshot', async () => {
    let resolve: (value: TestSetting) => void = () => undefined
    vi.mocked(intelligentTestsAPI.saveSetting).mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mount(TestSettingsPanel, { props: { settings: structuredClone(settings) } })
    const form = wrapper.findAll('form')[0]
    await form.trigger('submit'); await form.trigger('submit')
    expect(intelligentTestsAPI.saveSetting).toHaveBeenCalledTimes(1)
    expect(form.find('fieldset').attributes('disabled')).toBeDefined()
    const submitted = vi.mocked(intelligentTestsAPI.saveSetting).mock.calls[0][0]
    // Programmatic model changes cannot mutate the already submitted object.
    await form.find('textarea').setValue('A later unsent prompt')
    expect(submitted.config.prompt).toBe(settings[0].config.prompt)
    resolve(submitted); await flushPromises()
    expect(wrapper.emitted('saved')?.[0][0]).toMatchObject({ config: { prompt: settings[0].config.prompt } })
    expect(form.find('fieldset').attributes('disabled')).toBeUndefined()
    await form.find('textarea').setValue('Another edit after saving')
    expect(form.text()).not.toContain('已保存')
    wrapper.unmount()
  })
  it('keeps enabled and user-visible independent and never mutates incoming settings', async () => {
    vi.mocked(intelligentTestsAPI.saveSetting).mockImplementation(async item => item)
    const input = structuredClone(settings)
    const wrapper = mount(TestSettingsPanel, { props: { settings: input } })
    const form = wrapper.findAll('form')[0]
    await form.findAll('input[type=checkbox]')[1].setValue(true)
    expect(input[0].user_visible).toBe(false)
    await form.trigger('submit'); await flushPromises()
    expect(vi.mocked(intelligentTestsAPI.saveSetting).mock.calls[0][0]).toMatchObject({ enabled: true, user_visible: true })
    wrapper.unmount()
  })
  it('shows persisted running elapsed time, disables retry and includes risk', () => {
    const account: TestAccount = { account_id: 42, account_type: 'oauth', account_status: 'active', name: 'A', platform: 'openai', group_ids: [], anti_degradation: false, tests: [] }
    const wrapper = mount(TestResultCard, { props: { account, summary: { test_type: 'pelican', latest: record, history_count: 6, consecutive_anomalies: 2, risk: '当前账号防降智模式已关闭' }, now: Date.parse('2026-09-12T12:00:42Z') }, global })
    expect(wrapper.text()).toContain('已运行 42 秒')
    expect(wrapper.text()).toContain('历史 6')
    expect(wrapper.text()).toContain('当前账号防降智模式已关闭')
    expect(wrapper.findAll('button').at(-1)?.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})

describe('ordinary-user visibility', () => {
  it('finishes slow public polling without replacing the request every five seconds', async () => {
    vi.useFakeTimers()
    let finish!: (value: Awaited<ReturnType<typeof intelligentTestsAPI.publicAccounts>>) => void
    vi.mocked(intelligentTestsAPI.publicAccounts).mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(UserAccountCapabilities, { global }); await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(intelligentTestsAPI.publicAccounts).toHaveBeenCalledTimes(1)
    finish({ items: [{ account_id: 42, platform: 'openai', account_type: 'oauth', tests: [{ ...record, test_type: 'candy', status: 'completed' }] }], total: 1, page: 1, page_size: 12 }); await flushPromises()
    expect(wrapper.text()).toContain('oauth #42')
    wrapper.unmount()
  })
  it('renders no module when the server exposes no tests', async () => {
    vi.mocked(intelligentTestsAPI.publicAccounts).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 12 })
    const wrapper = mount(UserAccountCapabilities, { global }); await flushPromises()
    expect(wrapper.find('section').exists()).toBe(false)
    expect(intelligentTestsAPI.settings).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('public detail never includes admin actions or raw data', async () => {
    vi.mocked(intelligentTestsAPI.publicDetail).mockResolvedValue({ ...record, status: 'success', result: 'visible result' })
    const wrapper = mount(TestDetailDialog, { props: { recordId: 1, publicView: true }, global }); await flushPromises()
    expect(wrapper.text()).toContain('visible result')
    expect(wrapper.text()).not.toContain('重新测试')
    expect(wrapper.text()).not.toContain('原始响应')
    expect(wrapper.text()).not.toContain('检测时防降智')
    expect(intelligentTestsAPI.detail).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
