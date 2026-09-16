import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import AccountTrafficControls from '../AccountTrafficControls.vue'
import BaseDialog from '../../common/BaseDialog.vue'
import { accountTrafficAPI, defaultTrafficPolicy } from '@/api/admin/accountTraffic'
vi.mock('@/api/admin/accountTraffic', async () => ({ ...await vi.importActual('@/api/admin/accountTraffic'), accountTrafficAPI: { get: vi.fn(), save: vi.fn() } }))
const response = (enabled = false, limit = 128) => ({ policy: { ...defaultTrafficPolicy(), strict_rpm_enabled: enabled }, state_available: true, hard_limit: limit, state: { effective_concurrency: limit, recommended_concurrency: limit, in_flight: 0, requests_last_minute: 0, accepted: 0, rejected_rpm: 0, rejected_concurrency: 0, upstream_429: 0, upstream_5xx: 0, completed: 0, average_duration_ms: 0 } })
async function togglePanel(wrapper: ReturnType<typeof mount>, open: boolean) {
  (wrapper.get('details').element as HTMLDetailsElement).open = open
  await wrapper.get('details').trigger('toggle'); await flushPromises()
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
beforeEach(() => { vi.clearAllMocks(); vi.mocked(accountTrafficAPI.get).mockResolvedValue(response()); vi.mocked(accountTrafficAPI.save).mockResolvedValue(response(true)) })

describe('review reproductions: assertions express expected correct behavior', () => {
  it('old status refresh must not overwrite a just-saved enabled policy', async () => {
    const w = mount(AccountTrafficControls, { props: { accountId: 42 } }); await togglePanel(w, true)
    const old = deferred<ReturnType<typeof response>>()
    vi.mocked(accountTrafficAPI.get).mockReturnValueOnce(old.promise).mockResolvedValue(response(true))
    await w.findAll('button').find(b => b.text() === '刷新状态')!.trigger('click')
    await w.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await w.findAll('button').find(b => b.text() === '保存流量控制')!.trigger('click'); await flushPromises()
    old.resolve(response(false)); await flushPromises()
    const stale = w.text().includes('当前未启用流量控制')
    w.unmount(); expect(stale).toBe(false)
  })
  it('invalid independent traffic draft must not block saving the surrounding account form', async () => {
    const w = mount(defineComponent({ components: { AccountTrafficControls }, template: '<form><AccountTrafficControls :account-id="42" /><button type="submit">保存账号</button></form>' }))
    await togglePanel(w, true); await w.get('[data-testid=strict-rpm-toggle]').setValue(true)
    await w.findAll('input[type=number]')[0].setValue(0)
    await togglePanel(w, false)
    const valid = (w.get('form').element as HTMLFormElement).checkValidity()
    w.unmount(); expect(valid).toBe(true)
  })
  it('only the top nested dialog should respond to Escape', async () => {
    const w = mount(defineComponent({ components: { BaseDialog }, setup() { return { child: ref(true), parent: ref(true) } }, template: '<BaseDialog :show="parent" title="账号" @close="parent=false"><BaseDialog :show="child" title="策略预览" @close="child=false" /></BaseDialog>' }), { attachTo: document.body })
    await flushPromises(); document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); await flushPromises()
    const parentStillOpen = (w.vm as unknown as { parent: boolean }).parent
    w.unmount(); expect(parentStillOpen).toBe(true)
  })
  it('closing the child dialog should keep scrolling locked while the account dialog is open', async () => {
    const w = mount(defineComponent({ components: { BaseDialog }, setup() { return { child: ref(true) } }, template: '<BaseDialog :show="true" title="账号"><BaseDialog :show="child" title="策略预览" @close="child=false" /></BaseDialog>' }), { attachTo: document.body })
    await flushPromises()
    const dialogs = document.querySelectorAll('[role=dialog]')
    ;(dialogs[dialogs.length - 1].querySelector('button') as HTMLButtonElement).click(); await flushPromises()
    const locked = document.body.classList.contains('modal-open')
    w.unmount(); expect(locked).toBe(true)
  })
})

