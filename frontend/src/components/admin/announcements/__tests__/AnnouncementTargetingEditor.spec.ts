import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { AnnouncementCondition, AnnouncementTargeting } from '@/types'
import AnnouncementTargetingEditor from '../AnnouncementTargetingEditor.vue'

// 编辑器引用的 GroupSelector 会间接加载 i18n 实例，保留 vue-i18n 的其他导出
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
enableAutoUnmount(afterEach)

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue'],
  template: `<select class="select-stub" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
    <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
  </select>`
}
const UserPickerStub = {
  props: ['modelValue'],
  emits: ['update:modelValue'],
  template: `<div data-testid="user-picker" :data-ids="modelValue.join(',')">
    <button type="button" data-testid="user-picker-add" @click="$emit('update:modelValue', [...modelValue, 7])" />
  </div>`
}

const users = (...ids: number[]): AnnouncementCondition => ({ type: 'user', operator: 'in', user_ids: ids })
const rule = (...groups: AnnouncementCondition[][]): AnnouncementTargeting => ({
  any_of: groups.map((all_of) => ({ all_of }))
})

// 与父组件一样用 v-model：每次 emit 都回写 modelValue
function mountEditor(modelValue: AnnouncementTargeting) {
  const wrapper = mount(AnnouncementTargetingEditor, {
    props: {
      modelValue,
      groups: [],
      'onUpdate:modelValue': (value: AnnouncementTargeting) => wrapper.setProps({ modelValue: value })
    },
    global: {
      stubs: { Select: SelectStub, GroupSelector: true, Icon: true, AnnouncementUserPicker: UserPickerStub }
    }
  })
  return wrapper
}

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0))
  await flushPromises()
}

const lastEmitted = (wrapper: ReturnType<typeof mountEditor>) =>
  wrapper.emitted('update:modelValue')?.at(-1)?.[0] as AnnouncementTargeting | undefined

describe('AnnouncementTargetingEditor', () => {
  it('targets specific users through the dedicated mode', async () => {
    const wrapper = mountEditor({ any_of: [] })
    await wrapper.get('[data-testid="announcement-targeting-mode-users"]').trigger('change')
    await settle()
    expect(lastEmitted(wrapper)).toEqual(rule([users()]))
    expect(wrapper.find('[data-testid="announcement-targeting-users"]').text())
      .toContain('admin.announcements.form.selectUsersRequired')

    await wrapper.get('[data-testid="user-picker-add"]').trigger('click')
    await settle()
    expect(lastEmitted(wrapper)).toEqual(rule([users(7)]))
    expect(wrapper.text()).not.toContain('admin.announcements.form.selectUsersRequired')
  })

  it('opens a saved single-user announcement in specific-users mode', () => {
    const wrapper = mountEditor(rule([users(7, 9)]))
    expect(wrapper.find('[data-testid="announcement-targeting-custom"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="user-picker"]').attributes('data-ids')).toBe('7,9')
  })

  it('stays on custom rules once chosen, even when the rule only names one user', async () => {
    const wrapper = mountEditor(rule([users(7)]))
    await wrapper.get('[data-testid="announcement-targeting-mode-custom"]').trigger('change')
    await settle()
    expect(wrapper.find('[data-testid="announcement-targeting-custom"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="announcement-targeting-users"]').exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('keeps the already chosen users when switching from custom rules to specific users', async () => {
    const wrapper = mountEditor(rule(
      [users(7), { type: 'balance', operator: 'lt', value: 5 }],
      [{ type: 'subscription', operator: 'in', group_ids: [1] }]
    ))
    expect(wrapper.find('[data-testid="announcement-targeting-custom"]').exists()).toBe(true)

    await wrapper.get('[data-testid="announcement-targeting-mode-users"]').trigger('change')
    await settle()
    expect(lastEmitted(wrapper)).toEqual(rule([users(7)]))
  })

  it('offers specific users as a condition type in custom rules', async () => {
    const wrapper = mountEditor(rule([{ type: 'balance', operator: 'gte', value: 0 }]))
    await wrapper.get('select.select-stub').setValue('user')
    await settle()
    expect(lastEmitted(wrapper)).toEqual(rule([users()]))
    expect(wrapper.find('[data-testid="announcement-targeting-custom"] [data-testid="user-picker"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.announcements.form.selectUsersRequired')
  })
})
