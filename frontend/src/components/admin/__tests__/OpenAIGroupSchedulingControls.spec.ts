import { mount } from '@vue/test-utils'
import { describe, it, expect, vi } from 'vitest'
import Controls from '../OpenAIGroupSchedulingControls.vue'

vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn().mockResolvedValue([{ id: 2, name: '正常组' }, { id: 5, name: '降级组' }]) }))

describe('group scheduling rules', () => {
  it('serializes overrides, preserves zero and clears limits for legacy mode', async () => {
    const wrapper = mount(Controls, { props: { modelValue: '[{"group_id":5,"mode":"low_load","pre_output_seconds":25,"max_switches":5}]', 'onUpdate:modelValue': value => { wrapper.setProps({ modelValue: value }) } } })
    await wrapper.find('input').setValue('0')
    expect(JSON.parse(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)[0].pre_output_seconds).toBe(0)
    await wrapper.findAll('select')[1]!.setValue('legacy')
    expect(JSON.parse(wrapper.emitted('update:modelValue')!.at(-1)![0] as string)).toEqual([{ group_id: 5, mode: 'legacy' }])
    expect(wrapper.findAll('input')).toHaveLength(0)
    expect(wrapper.text()).toContain('原有逻辑')
  })
})
