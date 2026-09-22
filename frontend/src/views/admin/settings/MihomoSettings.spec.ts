import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MihomoSettings from './MihomoSettings.vue'
const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))
const base = { installed: false, supported: true, running: false, busy: false, nodes: 0, subscriptions: 0, phase: 'not_installed', endpoint: 'http://127.0.0.1:3101' }
describe('Mihomo settings', () => {
  it('shows subscription names but uses stable IDs for node actions', async () => {
    const state = { ...base, installed: true, running: true, nodes: 1, node_states: [{ name: 'node-hash', display_name: '日本 东京 01', state: 'enabled' }] }
    get.mockResolvedValue({ data: state }); post.mockResolvedValue({ data: state })
    const wrapper = mount(MihomoSettings)
    try {
      await flushPromises()
      expect(wrapper.text()).toContain('日本 东京 01')
      expect(wrapper.text()).not.toContain('node-hash')
      await wrapper.findAll('button').find(b => b.text() === '停用')!.trigger('click')
      await flushPromises()
      expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'disable/node-hash' }))
    } finally { wrapper.unmount() }
  })
  it('saves a country filter separately and preserves the subscription draft', async () => {
    const state={...base,installed:true,running:true,nodes:1,country_filter:{mode:'off',codes:[],allow_unknown:false},country_codes:['HK','US'],node_states:[{name:'node-one',state:'enabled',country_code:'HK'}]}
    get.mockResolvedValue({data:state});post.mockResolvedValue({data:state})
    const wrapper=mount(MihomoSettings);await flushPromises()
    await wrapper.get('textarea').setValue('https://example.org/unsaved')
    await wrapper.findAll('button').find(b=>b.text().includes('快捷'))!.trigger('click')
    await wrapper.findAll('button').find(b=>b.text()==='保存地区规则')!.trigger('click');await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo',expect.objectContaining({action:'country_filter',subscriptions:[],country_filter:{mode:'exclude',codes:['HK'],allow_unknown:false}}))
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('https://example.org/unsaved')
    wrapper.unmount()
  })
  it('explicitly enables use-once without replacing subscriptions', async () => {
    get.mockResolvedValue({data:{...base,installed:true,running:true,nodes:1,use_once:false,node_states:[{name:'node-one',state:'enabled'}]}})
    post.mockResolvedValue({data:{...base,installed:true,running:true,use_once:true}})
    const wrapper=mount(MihomoSettings);await flushPromises()
    await wrapper.get('textarea').setValue('https://example.org/unsaved')
    await wrapper.get('details input[type="checkbox"]').setValue(true);await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo',expect.objectContaining({action:'once_on',subscriptions:[]}))
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe('https://example.org/unsaved')
    wrapper.unmount()
  })
  beforeEach(() => { vi.resetAllMocks(); get.mockResolvedValue({ data: base }); post.mockResolvedValue({ data: base }) })
  it('installs only after explicit action and does not emit an unready proxy', async () => {
    const wrapper = mount(MihomoSettings); await flushPromises()
    expect(post).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(b => b.text() === '检测并安装')!.trigger('click'); await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'install' }))
    expect(wrapper.emitted('ready')).toBeUndefined(); wrapper.unmount()
  })
  it('submits multiple subscriptions without putting them in status output', async () => {
    get.mockResolvedValue({ data: { ...base, installed: true } })
    const wrapper = mount(MihomoSettings); await flushPromises()
    await wrapper.get('textarea').setValue('https://example.org/a?token=secret\nhttps://example.org/b')
    await wrapper.findAll('button').find(b => b.text() === '保存并应用')!.trigger('click'); await flushPromises()
    expect(post).toHaveBeenCalledWith('/admin/system/mihomo', expect.objectContaining({ action: 'apply', subscriptions: ['https://example.org/a?token=secret', 'https://example.org/b'] }))
    expect(wrapper.get<HTMLTextAreaElement>('textarea').element.value).toBe(''); wrapper.unmount()
  })
})
