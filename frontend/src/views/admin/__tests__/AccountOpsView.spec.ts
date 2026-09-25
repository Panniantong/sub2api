import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import AccountOpsView from '../AccountOpsView.vue'
import { getAccountOpsSettings, getAccountOpsEvents, saveAccountOpsSettings } from '@/api/admin/accountOps'
const state = vi.hoisted(() => ({ auth: null as any }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/admin/operations/SmartOpsNav.vue', () => ({ default: { template: '<nav />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/admin/accountOps', () => ({ getAccountOpsSettings: vi.fn(), getAccountOpsEvents: vi.fn(), saveAccountOpsSettings: vi.fn() }))
const config = { enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60 }
const settings = () => ({ config: { ...config }, smtp_configured: true, dropped_signals: 0, storage_failures: 0 })
const deferred = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
const create = (pinia = createPinia()) => mount(AccountOpsView, { global: { plugins: [pinia] } })
beforeEach(() => { vi.resetAllMocks(); state.auth = reactive({ user: { id: 1, role: 'admin' } }); vi.mocked(getAccountOpsSettings).mockResolvedValue(settings()); vi.mocked(getAccountOpsEvents).mockResolvedValue({ items: [], has_more: false }); vi.mocked(saveAccountOpsSettings).mockImplementation(async c => c) })
describe('account alert settings', () => {
  it('saves one global recipient with both explicitly selected alert categories', async () => {
    const wrapper = create(); await flushPromises()
    await wrapper.get('#account-ops-email').setValue('ops@example.com')
    await wrapper.get('[role="switch"]').setValue(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsSettings).toHaveBeenCalledWith({ ...config, recipient: 'ops@example.com', enabled: true })
    expect(wrapper.text()).toContain('qualityOps.saved'); wrapper.unmount()
  })
  it('retains an unsaved draft during refresh and across route re-entry', async () => {
    const pinia = createPinia(); const wrapper = create(pinia); await flushPromises()
    await wrapper.get('#account-ops-email').setValue('draft@example.com')
    await (wrapper.vm as any).load(); expect((wrapper.get('#account-ops-email').element as HTMLInputElement).value).toBe('draft@example.com')
    wrapper.unmount(); const again = create(pinia)
    expect((again.get('#account-ops-email').element as HTMLInputElement).value).toBe('draft@example.com')
    await flushPromises(); again.unmount()
  })
  it('ignores a stale settings response after save', async () => {
    const wrapper = create(); await flushPromises()
    const old = deferred<any>(); vi.mocked(getAccountOpsSettings).mockReturnValueOnce(old.promise)
    const refresh = (wrapper.vm as any).load()
    await wrapper.get('#account-ops-email').setValue('new@example.com'); await (wrapper.vm as any).save()
    old.resolve(settings()); await refresh
    expect((wrapper.get('#account-ops-email').element as HTMLInputElement).value).toBe('new@example.com'); wrapper.unmount()
  })
  it('does not restore a previous user snapshot after logout', async () => {
    const old = deferred<any>(); vi.mocked(getAccountOpsSettings).mockReturnValueOnce(old.promise)
    const wrapper = create(); state.auth.user = null
    old.resolve({ ...settings(), config: { ...config, recipient: 'private@example.com' } }); await flushPromises()
    expect(wrapper.find('#account-ops-email').exists()).toBe(false); expect(wrapper.text()).not.toContain('private@example.com'); wrapper.unmount()
  })
})
