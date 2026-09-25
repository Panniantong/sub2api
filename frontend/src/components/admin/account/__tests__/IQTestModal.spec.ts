import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import IQTestModal from '../IQTestModal.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

function streamResponse(events: Array<Record<string, unknown>>) {
  const encoder = new TextEncoder()
  const chunks = events.map((event) => encoder.encode(`data: ${JSON.stringify(event)}\n\n`))
  let index = 0
  return {
    ok: true,
    body: {
      getReader: () => ({
        read: vi.fn(async () => index < chunks.length
          ? { done: false, value: chunks[index++] }
          : { done: true, value: undefined })
      })
    }
  } as Response
}

function mountModal() {
  return mount(IQTestModal, {
    props: {
      show: true,
      account: {
        id: 42,
        name: 'Astra account',
        platform: 'openai',
        type: 'oauth',
        status: 'active'
      } as any
    },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Input: true,
        TextArea: true,
        Select: true,
        Icon: true
      }
    }
  })
}

describe('IQTestModal', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('auth_token', 'test-token')
    global.fetch = vi.fn(() => Promise.resolve(streamResponse([
      { type: 'test_start', model: 'gpt-6-astra' },
      { type: 'content', text: '<!doctype html><html><head><title>Pelican</title></head><body><svg></svg>' },
      { type: 'content', text: '<script>document.body.dataset.animated="true"</script></body></html>' },
      { type: 'test_complete', success: true }
    ]))) as any
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('uses the dedicated endpoint and sends identical settings to parallel runs', async () => {
    const wrapper = mountModal()
    ;(wrapper.vm as any).selectQuestion('pelican')
    ;(wrapper.vm as any).parallelCount = 2
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(global.fetch).toHaveBeenCalledTimes(2)
    for (const [url, request] of (global.fetch as any).mock.calls) {
      expect(url).toContain('/admin/accounts/42/pelican-test')
      const body = JSON.parse(request.body)
      expect(body).toMatchObject({
        model_id: 'gpt-6-astra',
        mode: 'default',
        reasoning_effort: 'medium'
      })
      expect(body.prompt).toContain('SVG 绘制一个鹈鹕骑自行车的 2D 动画')
      expect(body.prompt).toContain('直接返回独立 HTML')
    }

    const frames = wrapper.findAll('iframe')
    expect(frames).toHaveLength(2)
    expect(frames[0].attributes('srcdoc')).toContain('Content-Security-Policy')
    expect(frames[0].attributes('srcdoc')).toContain('<svg></svg>')
    expect(wrapper.text()).toContain('admin.accounts.pelicanTest.success')
    expect(localStorage.getItem('sub2api-pelican-test:42')).toContain('gpt-6-astra')
  })

  it('keeps non-HTML output visible but marks it as failed', async () => {
    global.fetch = vi.fn(() => Promise.resolve(streamResponse([
      { type: 'content', text: 'I cannot provide HTML.' },
      { type: 'test_complete', success: true }
    ]))) as any
    const wrapper = mountModal()
    ;(wrapper.vm as any).selectQuestion('pelican')
    await (wrapper.vm as any).startTest()
    await flushPromises()

    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('I cannot provide HTML.')
    expect(wrapper.text()).toContain('admin.accounts.pelicanTest.failed')
  })
  it('persists manual timing and model snapshots independently of later form edits', async () => {
    const wrapper = mountModal()
    ;(wrapper.vm as any).selectQuestion('pelican')
    await (wrapper.vm as any).startTest()
    const saved = JSON.parse(localStorage.getItem('sub2api-pelican-test:42')!)[0]
    expect(saved.runs[0]).toMatchObject({ source: 'manual', modelId: 'gpt-6-astra', reasoningEffort: 'medium' })
    expect(Number.isFinite(Date.parse(saved.runs[0].startedAt))).toBe(true)
    expect(saved.runs[0].durationMs).toBeGreaterThanOrEqual(0)
    ;(wrapper.vm as any).modelId = 'changed-model'
    await flushPromises()
    const metadata = wrapper.get('[data-testid="run-metadata"]').text()
    expect(metadata).toContain('gpt-6-astra')
    expect(metadata).not.toContain('changed-model')
    expect(metadata).toContain('admin.accounts.pelicanTest.sourceManual')
    wrapper.unmount()
  })

  it('shows server timing and saved settings when previewing a scheduled output', async () => {
    const wrapper = mountModal()
    ;(wrapper.vm as any).selectQuestion('pelican')
    ;(wrapper.vm as any).previewScheduled({ id: 9, status: 'success', response_text: '<html><body>pelican</body></html>', error_message: '', started_at: '2026-09-23T11:32:30Z', finished_at: '2026-09-23T11:34:42Z', latency_ms: 132100, pelican_config: { prompt: 'pelican', model_id: 'saved-model', reasoning_effort: 'high', parallel_count: 1 } })
    await flushPromises()
    const metadata = wrapper.get('[data-testid="run-metadata"]').text()
    expect(metadata).toContain('admin.accounts.pelicanTest.sourceScheduled')
    expect(metadata).toContain('saved-model / high')
    expect(metadata).toContain('132.1 s')
    expect(metadata).toContain('admin.accounts.pelicanTest.generatedAt')
    wrapper.unmount()
  })

})


describe('Intelligence question selection', () => {
  it('defaults to candy and accepts plain text without an HTML contract', async () => {
    global.fetch = vi.fn(() => Promise.resolve(streamResponse([
      { type: 'content', text: '21' }, { type: 'test_complete', success: true }
    ]))) as any
    const wrapper = mountModal()
    expect((wrapper.vm as any).questionKind).toBe('candy')
    await (wrapper.vm as any).startTest()
    await flushPromises()
    const body = JSON.parse((global.fetch as any).mock.calls[0][1].body)
    expect(body.prompt).toContain('圆形 7 9 8')
    expect(body.prompt).toContain('只输出最终整数')
    expect(body.prompt).not.toContain('独立 HTML')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('21')
    expect((wrapper.vm as any).runs[0].status).toBe('success')
    expect((wrapper.vm as any).records[0].questionKind).toBe('candy')
    ;(wrapper.vm as any).selectQuestion('pelican')
    ;(wrapper.vm as any).loadRecord((wrapper.vm as any).records[0])
    expect((wrapper.vm as any).questionKind).toBe('candy')
    wrapper.unmount()
  })
  it('previews scheduled candy results without marking text as invalid HTML', async () => {
    const wrapper = mountModal()
    ;(wrapper.vm as any).previewScheduled({ id: 1, status: 'success', response_text: '29', error_message: '', latency_ms: 100, started_at: new Date().toISOString(), pelican_config: { question_kind: 'candy', prompt: 'question', reasoning_effort: 'medium', parallel_count: 1 } })
    await flushPromises()
    expect((wrapper.vm as any).questionKind).toBe('candy')
    expect((wrapper.vm as any).runs[0].status).toBe('success')
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.text()).toContain('29')
    wrapper.unmount()
  })
})
