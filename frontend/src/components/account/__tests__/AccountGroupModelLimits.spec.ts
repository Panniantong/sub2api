import { describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import AccountGroupModelLimits from '../AccountGroupModelLimits.vue'

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: { modelValue: { type: Array, default: () => [] } },
  emits: ['update:modelValue'],
  template: '<div data-testid="model-selector">{{ modelValue.join(",") }}</div>'
})

const groups = [
  { id: 3, name: 'Group A' },
  { id: 5, name: 'Group B' }
]

function mountLimits(modelValue: Record<number, string[]>) {
  return mount(AccountGroupModelLimits, {
    props: { modelValue, groups, platform: 'openai', accountId: 1 },
    global: { stubs: { ModelWhitelistSelector: ModelWhitelistSelectorStub } }
  })
}

describe('AccountGroupModelLimits', () => {
  it('renders nothing when the account has no groups', () => {
    const wrapper = mount(AccountGroupModelLimits, {
      props: { modelValue: {}, groups: [] },
      global: { stubs: { ModelWhitelistSelector: ModelWhitelistSelectorStub } }
    })
    expect(wrapper.find('[data-testid="account-group-model-limits"]').exists()).toBe(false)
  })

  it('shows the picker only for limited groups', () => {
    const wrapper = mountLimits({ 5: ['gpt-5.5'] })
    expect(wrapper.get('[data-testid="group-model-limit-3"]').find('[data-testid="model-selector"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="group-model-limit-5"]').get('[data-testid="model-selector"]').text()).toBe('gpt-5.5')
  })

  it('switching a group to selected models starts from an empty list', async () => {
    const wrapper = mountLimits({ 5: ['gpt-5.5'] })
    await wrapper.get('[data-testid="group-model-limit-3"]').get('[data-testid="group-model-limit-selected"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ 3: [], 5: ['gpt-5.5'] }])
  })

  it('warns that an empty selection saves as unrestricted', () => {
    const wrapper = mountLimits({ 3: [] })
    expect(wrapper.get('[data-testid="group-model-limit-3"]').find('[data-testid="group-model-limit-empty"]').exists()).toBe(true)
  })

  it('switching back to all models removes the group limit', async () => {
    const wrapper = mountLimits({ 3: ['gpt-5.4'], 5: ['gpt-5.5'] })
    await wrapper.get('[data-testid="group-model-limit-5"]').get('[data-testid="group-model-limit-all"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ 3: ['gpt-5.4'] }])
  })

  it('forwards picker changes for the right group', async () => {
    const wrapper = mountLimits({ 5: ['gpt-5.5'] })
    wrapper.getComponent(ModelWhitelistSelectorStub).vm.$emit('update:modelValue', ['gpt-5.5', 'gpt-5.4'])
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([{ 5: ['gpt-5.5', 'gpt-5.4'] }])
  })
})
