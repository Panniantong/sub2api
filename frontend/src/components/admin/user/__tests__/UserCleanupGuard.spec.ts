import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UserCleanupGuard from '../UserCleanupGuard.vue'

const { getGuard, updateGuard } = vi.hoisted(() => ({ getGuard: vi.fn(), updateGuard: vi.fn() }))
vi.mock('@/api/admin/userCleanup', () => ({ userCleanupAPI: { getGuard, updateGuard } }))
const setup = (role = 'user', canMarkSystem = false) => mount(UserCleanupGuard, { props: { userId: 7, role, canMarkSystem } })

describe('UserCleanupGuard', () => {
  beforeEach(() => { vi.clearAllMocks(); getGuard.mockResolvedValue({ user_id: 7, is_system: false, is_protected: true, role_protected: false }); updateGuard.mockResolvedValue({ user_id: 7, is_system: false, is_protected: false, role_protected: false }) })

  it('requires confirmation before removing protection', async () => {
    const wrapper = setup(); await flushPromises()
    await wrapper.get('input').trigger('click')
    expect(updateGuard).not.toHaveBeenCalled(); expect(wrapper.text()).toContain('确认取消保护')
    const confirm = wrapper.findAll('button').find(button => button.text() === '确认取消保护')!
    await confirm.trigger('click'); await flushPromises()
    expect(updateGuard).toHaveBeenCalledWith(7, false, undefined); expect(wrapper.text()).toContain('保护设置已保存')
  })

  it('hides system controls from ordinary administrators', async () => {
    const wrapper = setup(); await flushPromises(); expect(wrapper.findAll('input')).toHaveLength(1)
    await wrapper.setProps({ canMarkSystem: true }); expect(wrapper.findAll('input')).toHaveLength(2)
  })

  it('does not fetch or expose editable protection for administrator roles', async () => {
    const wrapper = setup('super_admin'); await flushPromises(); expect(getGuard).not.toHaveBeenCalled(); expect(wrapper.find('input').exists()).toBe(false)
  })

  it('does not assume unprotected when loading fails', async () => {
    getGuard.mockRejectedValue(new Error('读取失败'))
    const wrapper = setup(); await flushPromises(); expect(wrapper.text()).toContain('读取失败'); expect(wrapper.find('input').exists()).toBe(false)
  })
})
