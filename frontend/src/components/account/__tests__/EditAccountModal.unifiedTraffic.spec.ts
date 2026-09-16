import { defineComponent } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account } from '@/types'
const mocks = vi.hoisted(() => ({ update: vi.fn(), apply: vi.fn(), preview: vi.fn(), trafficGet: vi.fn(), trafficSave: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { update: mocks.update, applyAntiDegrade: mocks.apply, previewAntiDegrade: mocks.preview, checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }) }, settings: { getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }), getSettings: vi.fn().mockResolvedValue({}) }, tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/api/admin/accounts', () => ({ getAntigravityDefaultModelMapping: vi.fn() }))
vi.mock('@/api/admin/accountTraffic', async () => ({ ...await vi.importActual('@/api/admin/accountTraffic'), accountTrafficAPI: { get: mocks.trafficGet, save: mocks.trafficSave } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
import EditAccountModal from '../EditAccountModal.vue'
import AccountTrafficControls from '../AccountTrafficControls.vue'
import { defaultTrafficPolicy } from '@/api/admin/accountTraffic'
const dialog = defineComponent({ props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' })
const account = (extra: Record<string, unknown> = {}) => ({ id: 42, name: 'Review fixture', platform: 'openai', type: 'oauth', credentials: {}, extra, proxy_id: null, concurrency: 128, priority: 1, rate_multiplier: 1, status: 'active', group_ids: [], expires_at: null, auto_pause_on_expired: false, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T01:00:00Z' } as Account)
function modal(value = account()) { return shallowMount(EditAccountModal, { props: { show: true, account: value, groups: [], proxies: [] }, global: { stubs: { BaseDialog: dialog, AccountTrafficControls: false } } }) }
async function openTraffic(w: ReturnType<typeof modal>) { const d = w.get('details[data-testid=account-traffic-controls]'); (d.element as HTMLDetailsElement).open = true; await d.trigger('toggle'); await flushPromises() }
beforeEach(() => {
  vi.clearAllMocks()
  mocks.update.mockImplementation(async (_id, payload) => ({ ...account(payload.extra), ...payload }))
  mocks.trafficGet.mockResolvedValue({ policy: defaultTrafficPolicy(), state_available: true, hard_limit: 128, state: null })
  mocks.trafficSave.mockImplementation(async (_id, policy) => ({ policy, hard_limit: 128, state_available: true }))
  mocks.preview.mockResolvedValue({ account_id: 42, eligible: true, enabled: false, changes: [] })
  mocks.apply.mockResolvedValue(account())
})
describe('account save review: expected invariants', () => {
  it('fresh same-account props must replace older independently saved traffic settings on reopen', async () => {
    const old = { ...defaultTrafficPolicy(), strict_rpm_enabled: true }
    const fresh = { ...defaultTrafficPolicy(), strict_rpm_enabled: false }
    const w = modal(account({ account_traffic_control: old }))
    w.findComponent(AccountTrafficControls).vm.$emit('saved', old)
    await w.setProps({ show: false })
    await w.setProps({ account: { ...account({ account_traffic_control: fresh }), updated_at: '2026-09-15T02:00:00Z' }, show: true })
    await w.get('#edit-account-form').trigger('submit'); await flushPromises()
    const sent = mocks.update.mock.calls.at(-1)![1]
    w.unmount(); expect(sent.extra.account_traffic_control.strict_rpm_enabled).toBe(false)
  })
  it('ordinary save must not close and discard an unsaved traffic draft', async () => {
    const w = modal(); await openTraffic(w)
    await w.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await w.get('#edit-account-form').trigger('submit'); await flushPromises()
    const saved = mocks.trafficSave.mock.calls.length > 0 || mocks.update.mock.calls.some(([, payload]) => payload.extra?.account_traffic_control?.strict_rpm_enabled)
    const discarded = !saved && (w.emitted('close')?.length ?? 0) > 0
    w.unmount(); expect(discarded).toBe(false)
  })
  it("submits traffic and concurrency once with the account and blocks duplicate saves", async () => {
    let resolve!: (value: unknown) => void
    mocks.update.mockReturnValue(new Promise(r => { resolve = r }))
    const w = modal(); await openTraffic(w)
    expect(w.findAll("button").some(b => b.text() === "保存流量控制")).toBe(false)
    await w.get("[data-testid=strict-rpm-toggle]").setValue(true)
    await w.get("#edit-account-form").trigger("submit"); await flushPromises()
    await w.get("#edit-account-form").trigger("submit"); await flushPromises()
    expect(mocks.update).toHaveBeenCalledTimes(1)
    expect(mocks.update.mock.calls[0][1].extra.account_traffic_control.strict_rpm_enabled).toBe(true)
    expect(mocks.trafficSave).not.toHaveBeenCalled()
    resolve(account()); await flushPromises(); w.unmount()
  })
  it('switching identity should not reset an unsaved concurrency edit', async () => {
    const w = modal()
    const input = w.findAll('input[type=number]').find(i => (i.element as HTMLInputElement).value === '128')!
    await input.setValue(256)
    await w.get('[data-testid=anti-degrade-mode-1]').trigger('click'); await flushPromises()
    await w.get('[data-testid=anti-degrade-confirm]').trigger('click'); await flushPromises()
    const number = (input.element as HTMLInputElement).value
    w.unmount(); expect(number).toBe('256')
  })
  it('uses the edited concurrency ceiling when validating the same traffic submission', async () => {
    const w = modal(); await openTraffic(w)
    const concurrency = w.findAll('input[type=number]').find(i => (i.element as HTMLInputElement).value === '128')!
    await concurrency.setValue(256)
    await w.get('[data-testid=adaptive-toggle]').setValue(true)
    const min = w.findComponent(AccountTrafficControls).findAll('input[type=number]')[0]
    await min.setValue(192)
    await w.get('#edit-account-form').trigger('submit'); await flushPromises()
    expect(mocks.update.mock.calls[0][1]).toMatchObject({ concurrency: 256, extra: { account_traffic_control: { min_concurrency: 192, adaptive_enabled: true } } })
    w.unmount()
  })
  it('keeps drafts on tab changes and asks before closing an unsaved editor', async () => {
    const w = modal()
    await w.get('[data-tour=edit-account-form-name]').setValue('Edited name')
    await w.get('[data-testid=account-section-protection]').trigger('click')
    await w.get('[data-testid=account-section-basic]').trigger('click')
    expect((w.get('[data-tour=edit-account-form-name]').element as HTMLInputElement).value).toBe('Edited name')
    await w.findAll('button').find(b => b.text() === 'common.cancel')!.trigger('click')
    expect(w.emitted('close')).toBeUndefined()
    const confirm = w.findAllComponents({ name: 'ConfirmDialog' }).find(c => c.props('title') === '放弃未保存的修改？')!
    expect(confirm.props('show')).toBe(true)
    w.unmount()
  })
})
