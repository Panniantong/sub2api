import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import Panel from './MihomoCountryFilter.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))
const nodes = [{ name: 'hk', state: 'enabled', country_code: 'HK' }, { name: 'us', state: 'enabled', country_code: 'US' }, { name: 'unknown', state: 'enabled' }, { name: 'retired', state: 'used', country_code: 'US' }]
const props = { codes: ['HK', 'US', 'SG'], nodes, busy: false }
describe('Country filter panel', () => {
  it('excludes Hong Kong and unknown regions explicitly, preserving retired nodes', async () => {
    const wrapper = mount(Panel, { props })
    await wrapper.findAll('button').find(b => b.text().includes('快捷'))!.trigger('click')
    expect(wrapper.text()).toContain('预计可用 1 / 4')
    await wrapper.findAll('button').find(b => b.text() === '保存地区规则')!.trigger('click')
    expect(wrapper.emitted('save')?.[0]).toEqual([{ mode: 'exclude', codes: ['HK'], allow_unknown: false }])
  })
  it('supports an allowlist and an explicit unknown-region exception', async () => {
    const wrapper = mount(Panel, { props })
    await wrapper.get('select').setValue('include')
    await wrapper.get('[data-country="US"]').trigger('click')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    expect(wrapper.text()).toContain('预计可用 2 / 4')
    await wrapper.findAll('button').find(b => b.text() === '保存地区规则')!.trigger('click')
    expect(wrapper.emitted('save')?.[0]).toEqual([{ mode: 'include', codes: ['US'], allow_unknown: true }])
  })
  it('keeps unsaved choices across polling and supports English search', async () => {
    const wrapper = mount(Panel, { props })
    await wrapper.get('select').setValue('exclude')
    await wrapper.get('[data-country="HK"]').trigger('click')
    await wrapper.setProps({ filter: { mode: 'off', codes: [], allow_unknown: false } })
    expect(wrapper.get<HTMLSelectElement>('select').element.value).toBe('exclude')
    await wrapper.get('input[type="search"]').setValue('Hong Kong')
    expect(wrapper.find('[data-country="HK"]').exists()).toBe(true)
    expect(wrapper.find('[data-country="US"]').exists()).toBe(false)
  })
})
