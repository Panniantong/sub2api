import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelTagInput from '../ModelTagInput.vue'

function mountInput(modelValue: string[] = []) {
  return mount(ModelTagInput, {
    props: { modelValue, candidates: ['gpt-6-luna', 'gpt-6-sol'] },
    global: { stubs: { Icon: true } }
  })
}

const lastEmitted = (wrapper: ReturnType<typeof mountInput>) =>
  wrapper.emitted('update:modelValue')?.at(-1)?.[0]

describe('ModelTagInput', () => {
  it('adds typed models on Enter and splits pasted lists', async () => {
    const wrapper = mountInput()
    const draft = wrapper.get('[data-testid="model-tag-draft"]')
    await draft.setValue('gpt-6-luna, gpt-6-*  claude-opus-*')
    await draft.trigger('keydown', { key: 'Enter' })
    expect(lastEmitted(wrapper)).toEqual(['gpt-6-luna', 'gpt-6-*', 'claude-opus-*'])
  })

  it('ignores duplicates regardless of case', async () => {
    const wrapper = mountInput(['gpt-6-luna'])
    const draft = wrapper.get('[data-testid="model-tag-draft"]')
    await draft.setValue('GPT-6-LUNA')
    await draft.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('does not commit while an IME composition is active', async () => {
    const wrapper = mountInput()
    const draft = wrapper.get('[data-testid="model-tag-draft"]')
    await draft.setValue('gpt-6-luna')
    await draft.trigger('keydown', { key: 'Enter', isComposing: true })
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('removes a model with its button or Backspace on an empty draft', async () => {
    const wrapper = mountInput(['gpt-6-luna', 'gpt-6-sol'])
    await wrapper.get('button[aria-label="remove gpt-6-luna"]').trigger('click')
    expect(lastEmitted(wrapper)).toEqual(['gpt-6-sol'])

    await wrapper.get('[data-testid="model-tag-draft"]').trigger('keydown', { key: 'Backspace' })
    expect(lastEmitted(wrapper)).toEqual(['gpt-6-luna'])
  })

  it('suggests only candidates that are not picked yet', () => {
    const wrapper = mountInput(['gpt-6-luna'])
    expect(wrapper.findAll('datalist option').map(o => o.attributes('value'))).toEqual(['gpt-6-sol'])
  })
})
