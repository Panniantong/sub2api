import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { Account } from '@/types'
import type { AntiDegradePreview } from '@/api/admin/accounts'

const mocks = vi.hoisted(() => ({
  preview: vi.fn(), apply: vi.fn(), revert: vi.fn(), update: vi.fn(),
  success: vi.fn(), info: vi.fn(), error: vi.fn()
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success, showInfo: mocks.info }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: true }) }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      previewAntiDegrade: mocks.preview, applyAntiDegrade: mocks.apply,
      revertAntiDegrade: mocks.revert, update: mocks.update,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: { list: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/api/admin/accounts', () => ({ getAntigravityDefaultModelMapping: vi.fn() }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  props: ['show', 'title'],
  template: '<div v-if="show" :data-title="title"><slot /><slot name="footer" /></div>'
})
const account = (extra: Record<string, unknown> = {}, overrides: Partial<Account> = {}) => ({
  id: 42, name: 'Local test account', platform: 'openai', type: 'oauth',
  credentials: {}, extra, proxy_id: null, concurrency: 10, priority: 1,
  rate_multiplier: 1, status: 'active', group_ids: [], expires_at: null,
  auto_pause_on_expired: false, updated_at: '2026-09-11T01:00:00Z', ...overrides
} as Account)
const policy = (mode = 'mode1') => ({
  anti_degrade: { enabled: true, mode, policy_version: mode === 'mode1' ? 2 : 1, max_concurrency: 2 },
  codex_fingerprint_mode: mode === 'mode1' ? 'device' : 'session',
  enable_tls_fingerprint: true,
  tls_fingerprint_builtin: mode === 'mode1' ? 'nodejs24' : 'nodejs22'
})
const preview = (overrides: Partial<AntiDegradePreview> = {}): AntiDegradePreview => ({
  account_id: 42, eligible: true, enabled: false, active_mode: '',
  policy_version: 0, identity_ready: false, tls_profile: '', changes: [], ...overrides
})
const mountModal = (value = account()) => shallowMount(EditAccountModal, {
  props: { show: true, account: value, groups: [], proxies: [] },
  global: { stubs: { BaseDialog: BaseDialogStub } }
})
const openPreview = async (wrapper: ReturnType<typeof mountModal>, mode = 1) => {
  await wrapper.get(`[data-testid="anti-degrade-mode-${mode}"]`).trigger('click')
  await flushPromises()
}
const apply = async (wrapper: ReturnType<typeof mountModal>) => {
  await wrapper.get('[data-testid="anti-degrade-confirm"]').trigger('click')
  await flushPromises()
}

describe('EditAccountModal account protection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.preview.mockResolvedValue(preview())
    mocks.apply.mockResolvedValue(account(policy(), { concurrency: 2 }))
    mocks.revert.mockResolvedValue(account())
    mocks.update.mockImplementation(async (_id, payload) => account(payload.extra, { concurrency: payload.concurrency }))
  })

  it('keeps both mode buttons available, including previewing the active mode', async () => {
    const wrapper = mountModal(account(policy()))
    expect(wrapper.get('[data-testid="anti-degrade-status"]').text()).toContain('admin.accounts.antiDegradeEnabled')
    expect(mocks.preview).not.toHaveBeenCalled()
    for (const mode of [1, 2]) {
      expect(wrapper.get(`[data-testid="anti-degrade-mode-${mode}"]`).attributes('disabled')).toBeUndefined()
      await openPreview(wrapper, mode)
      expect(mocks.preview).toHaveBeenLastCalledWith(42, `mode${mode}`)
    }
    expect(wrapper.text()).toContain('admin.accounts.antiDegradeMode2Desc')
    wrapper.unmount()
  })

  it('lets legacy observe request integrity without switching identity or TLS', async () => {
    const wrapper = mountModal(account(policy('legacy')))
    const control = wrapper.get('[data-testid="request-integrity-control"] select')
    expect(control.attributes('disabled')).toBeUndefined()
    await control.setValue('observe')
    await wrapper.get('#edit-account-form').trigger('submit'); await flushPromises()
    expect(mocks.update).toHaveBeenCalled()
    const sent = mocks.update.mock.calls.at(-1)![1]
    expect(sent.extra.request_integrity_mode).toBe('observe')
    expect(sent.extra.anti_degrade.mode).toBe('legacy')
    expect(mocks.apply).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('lets the administrator explicitly disable mode1 request integrity', async () => {
    const wrapper = mountModal(account(policy()))
    const select = wrapper.get('[data-testid="request-integrity-control"] select')
    expect(select.attributes('disabled')).toBeUndefined()
    await select.setValue('off')
    await wrapper.get('#edit-account-form').trigger('submit'); await flushPromises()
    expect(mocks.update.mock.calls.at(-1)![1].extra.request_integrity_mode).toBe('off')
    wrapper.unmount()
  })

  it('allows a concurrency value above strategy presets without changing the strategy', async () => {
    const wrapper = mountModal(account(policy('legacy')))
    const input = wrapper.findAll('input[type=number]').find(item => (item.element as HTMLInputElement).value === '10')!
    expect(input.attributes('max')).toBeUndefined()
    await input.setValue(128)
    await wrapper.get('#edit-account-form').trigger('submit'); await flushPromises()
    expect(mocks.update.mock.calls.at(-1)![1].concurrency).toBe(128)
    expect(mocks.apply).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('exposes the original sub2 strategy as an explicit third option', async () => {
    const wrapper = mountModal(account(policy('legacy')))
    const legacy = wrapper.get('[data-testid="anti-degrade-mode-legacy"]')
    expect(legacy.text()).toContain('默认')
    expect(wrapper.get('[data-testid="anti-degrade-strategies"] button').attributes('data-testid')).toBe('anti-degrade-mode-legacy')
    expect(legacy.attributes('disabled')).toBeUndefined()
    await legacy.trigger('click')
    expect(mocks.preview).toHaveBeenLastCalledWith(42, 'legacy')
    expect(wrapper.text()).toContain('admin.accounts.antiDegradeModeLegacyDesc')
    wrapper.unmount()
  })

  it('shows backend policy details and issues without claiming enabled markers are healthy', async () => {
    mocks.preview.mockResolvedValue(preview({ enabled: true, active_mode: 'mode1', policy_version: 2, identity_ready: true, tls_profile: 'nodejs24', issues: ['全局 TLS 未启用'], changes: [{ key: 'extra.codex_fingerprint_seed', to: 'DO-NOT-DISPLAY' }] }))
    const wrapper = mountModal(account(policy()))
    await openPreview(wrapper)
    const state = wrapper.get('[data-testid="anti-degrade-preview-state"]').text()
    expect(state).toContain('admin.accounts.antiDegradeConfigurationIssue')
    expect(state).toContain('admin.accounts.antiDegradeMode1')
    expect(state).toContain('2')
    expect(state).toContain('nodejs24')
    expect(state).toContain('admin.accounts.antiDegradeIdentityReady')
    expect(wrapper.get('[data-testid="anti-degrade-issues"]').text()).toContain('全局 TLS 未启用')
    expect(wrapper.text()).not.toContain('DO-NOT-DISPLAY')
    wrapper.unmount()
  })

  it('only labels verified configuration as enabled and detects missing identity or TLS', async () => {
    mocks.preview.mockResolvedValue(preview({ enabled: true, active_mode: 'mode1', policy_version: 2, identity_ready: true, tls_profile: 'nodejs24' }))
    const wrapper = mountModal(account(policy()))
    await openPreview(wrapper)
    expect(wrapper.get('[data-testid="anti-degrade-status"]').text()).toBe('admin.accounts.antiDegradeEnabled')
    mocks.preview.mockResolvedValue(preview({ enabled: true, active_mode: 'mode1', policy_version: 2, identity_ready: false, tls_profile: '' }))
    await openPreview(wrapper)
    expect(wrapper.get('[data-testid="anti-degrade-status"]').text()).toBe('admin.accounts.antiDegradeConfigurationIssue')
    wrapper.unmount()
  })

  it('does not force the switch on when apply returns a disabled account', async () => {
    mocks.apply.mockResolvedValue(account({ anti_degrade: { enabled: false } }))
    const wrapper = mountModal()
    await openPreview(wrapper)
    await apply(wrapper)
    expect(mocks.apply).toHaveBeenCalledWith(42, 'mode1')
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="anti-degrade-status"]').exists()).toBe(false)
    expect(mocks.success).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows v2 migration and recognizes v3 standard transport as configured', async () => {
    mocks.preview.mockResolvedValue(preview({ enabled: true, active_mode: 'mode1', policy_version: 2, identity_ready: true, tls_profile: 'standard', reason: 'Upgrade to v3; preserve original snapshot', changes: [{ key: 'policy_version', from: 2, to: 3 }] }))
    const wrapper = mountModal(account(policy()))
    await openPreview(wrapper)
    expect(wrapper.text()).toContain('Upgrade to v3; preserve original snapshot')
    expect(wrapper.find('[data-testid="anti-degrade-confirm"]').exists()).toBe(true)
    mocks.preview.mockResolvedValue(preview({ enabled: true, eligible: false, active_mode: 'mode1', policy_version: 3, identity_ready: true, tls_profile: 'standard' }))
    await openPreview(wrapper)
    expect(wrapper.get('[data-testid="anti-degrade-status"]').text()).toBe('admin.accounts.antiDegradeEnabled')
    expect(wrapper.find('[data-testid="anti-degrade-confirm"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps returned mode1 fields across stale prop replacement, reopening and ordinary save', async () => {
    const stale = account({ enable_tls_fingerprint: false, tls_fingerprint_builtin: 'nodejs22', tls_fingerprint_profile_id: 9, codex_fingerprint_seed: 'SERVER-ONLY' })
    const wrapper = mountModal(stale)
    await openPreview(wrapper)
    await apply(wrapper)
    await wrapper.setProps({ account: { ...stale } })
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.find('[data-testid="anti-degrade-preview-state"]').exists()).toBe(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.concurrency).toBe(2)
    expect(payload.extra).toMatchObject(policy())
    expect(payload.extra).not.toHaveProperty('tls_fingerprint_profile_id')
    expect(payload.extra).not.toHaveProperty('codex_fingerprint_seed')
    wrapper.unmount()
  })

  it('switches to mode2 and restores complete server values on revert despite older props', async () => {
    mocks.apply.mockResolvedValue(account(policy('mode2'), { concurrency: 1 }))
    const wrapper = mountModal(account(policy()))
    await openPreview(wrapper, 2)
    await apply(wrapper)
    expect(mocks.apply).toHaveBeenCalledWith(42, 'mode2')
    await wrapper.get('[data-testid="anti-degrade-revert"]').trigger('click')
    expect(mocks.revert).not.toHaveBeenCalled()
    const confirmation = wrapper.findAllComponents({ name: 'ConfirmDialog' }).find(dialog => dialog.props('title') === '关闭账号保护？')!
    expect(confirmation.props('show')).toBe(true)
    confirmation.vm.$emit('confirm')
    await flushPromises()
    expect(mocks.revert).toHaveBeenCalledWith(42, true)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    const payload = mocks.update.mock.calls[0][1]
    expect(payload.concurrency).toBe(10)
    for (const key of Object.keys(policy())) expect(payload.extra).not.toHaveProperty(key)
    wrapper.unmount()
  })

  it('uses the returned marker when revert does not disable the configuration', async () => {
    mocks.revert.mockResolvedValue(account(policy()))
    const wrapper = mountModal(account(policy()))
    await wrapper.get('[data-testid="anti-degrade-revert"]').trigger('click')
    expect(mocks.revert).not.toHaveBeenCalled()
    const confirmation = wrapper.findAllComponents({ name: 'ConfirmDialog' }).find(dialog => dialog.props('title') === '关闭账号保护？')!
    expect(confirmation.props('show')).toBe(true)
    confirmation.vm.$emit('confirm')
    await flushPromises()
    expect(mocks.revert).toHaveBeenCalledWith(42, true)
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    expect(mocks.success).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('retains an apply response arriving while closed and prevents overlapping ordinary save', async () => {
    let resolve!: (value: Account) => void
    mocks.apply.mockReturnValue(new Promise<Account>(done => { resolve = done }))
    const wrapper = mountModal()
    await openPreview(wrapper)
    await wrapper.get('[data-testid="anti-degrade-confirm"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    expect(mocks.update).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    resolve(account(policy(), { concurrency: 2 }))
    await flushPromises()
    await wrapper.setProps({ show: true })
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="anti-degrade-mode-1"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('clears returned policy overrides for another account and accepts genuinely newer props', async () => {
    const wrapper = mountModal()
    await openPreview(wrapper)
    await apply(wrapper)
    await wrapper.setProps({ account: account({}, { updated_at: '2026-09-11T02:00:00Z' }) })
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    await openPreview(wrapper)
    await apply(wrapper)
    await wrapper.setProps({ account: account({}, { id: 43 }) })
    expect(wrapper.get('[data-testid="anti-degrade-toggle"]').attributes('aria-checked')).toBe('false')
    wrapper.unmount()
  })

  it('does not show an unsupported apply action and ignores preview completion after close', async () => {
    mocks.preview.mockResolvedValue(preview({ eligible: false, reason: '模式一仅支持 OpenAI OAuth / setup-token' }))
    const wrapper = mountModal(account({}, { type: 'apikey' }))
	 expect(wrapper.find('[data-testid="anti-degrade-mode-1"]').exists()).toBe(false)
	 await wrapper.setProps({ account: account() })
    await openPreview(wrapper)
    expect(wrapper.text()).toContain('模式一仅支持 OpenAI OAuth / setup-token')
    expect(wrapper.find('[data-testid="anti-degrade-confirm"]').exists()).toBe(false)
    let resolve!: (value: AntiDegradePreview) => void
    mocks.preview.mockReturnValue(new Promise<AntiDegradePreview>(done => { resolve = done }))
    await wrapper.get('[data-testid="anti-degrade-mode-1"]').trigger('click')
    await wrapper.setProps({ show: false })
    resolve(preview())
    await flushPromises()
    await wrapper.setProps({ show: true })
    expect(wrapper.find('[data-testid="anti-degrade-preview-state"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
