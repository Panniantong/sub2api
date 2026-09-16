import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { Account } from '@/types'
import ProtectionToggle from '../ProtectionToggle.vue'

const mocks = vi.hoisted(() => ({ set: vi.fn(), success: vi.fn(), error: vi.fn(), admin: true }))
vi.mock('@/api/admin/accounts', () => ({ setProtection: mocks.set }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: mocks.admin }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.success, showError: mocks.error }) }))
const account = (enabled = true) => ({ id: 6, anti_degradation: enabled, protection_scope: 'generic_v1', extra: {} } as Account)

describe('ProtectionToggle', () => {
  beforeEach(() => { vi.clearAllMocks(); mocks.admin = true; mocks.set.mockResolvedValue(account(false)) })
  it('requires confirmation before sending ON to OFF and preserves state on cancel', async () => {
    const wrapper = shallowMount(ProtectionToggle, { props: { account: account() } })
    await wrapper.get('button').trigger('click')
    expect(mocks.set).not.toHaveBeenCalled()
    const confirm = wrapper.findComponent({ name: 'ConfirmDialog' })
    expect(confirm.props('show')).toBe(true)
    confirm.vm.$emit('cancel')
    await flushPromises()
    expect(mocks.set).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    confirm.vm.$emit('confirm')
    await flushPromises()
    expect(mocks.set).toHaveBeenCalledWith(6, false, true)
    expect(wrapper.emitted('updated')?.[0]).toEqual([account(false)])
  })
  it('discloses the generic capability boundary and does not mislabel it as Codex', () => {
    const wrapper = shallowMount(ProtectionToggle, { props: { account: account() } })
    expect(wrapper.get('button').attributes('title')).toContain('暂不支持 Codex')
    expect(wrapper.text()).toContain('并发保护 已开启')
  })
  it('blocks ordinary users and prevents double submission', async () => {
    mocks.admin = false
    const user = shallowMount(ProtectionToggle, { props: { account: account(false) } })
    expect(user.get('button').attributes('disabled')).toBeDefined()
    user.unmount()
    mocks.admin = true
    let resolve!: (value: Account) => void
    mocks.set.mockReturnValue(new Promise<Account>(done => { resolve = done }))
    const admin = shallowMount(ProtectionToggle, { props: { account: account(false) } })
    await admin.get('button').trigger('click')
    await admin.get('button').trigger('click')
    expect(mocks.set).toHaveBeenCalledTimes(1)
    resolve(account())
    await flushPromises()
  })
})
