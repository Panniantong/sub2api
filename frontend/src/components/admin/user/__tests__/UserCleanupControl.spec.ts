import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UserCleanupControl from '../UserCleanupControl.vue'

const { summary, preview, execute, updateGuard } = vi.hoisted(() => ({ summary: vi.fn(), preview: vi.fn(), execute: vi.fn(), updateGuard: vi.fn() }))
vi.mock('@/api/admin/userCleanup', () => ({ userCleanupAPI: { summary, preview, execute, updateGuard } }))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))

const snapshot = () => ({
  total: 2, zero_balance: 1, negative_balance: 1, server_time: '2026-09-12T12:00:00Z', cutoff: '2026-09-11T12:00:00Z', batch_limit: 500,
  preview_id: '11111111-1111-4111-8111-111111111111', expires_at: '2026-09-12T12:10:00Z',
  candidates: [
    { id: 7, email: 'old@example.test', username: 'Old', balance: '0.00000000', zero_balance: true, last_used_at: '2026-09-10T10:00:00Z', created_at: '2026-08-01T10:00:00Z' },
    { id: 8, email: 'negative@example.test', username: '', balance: '-0.00000001', zero_balance: false, last_used_at: '2026-09-10T10:00:00Z', created_at: '2026-08-01T10:00:00Z' }
  ]
})
const setup = () => mount(UserCleanupControl, { global: { stubs: {
  BaseDialog: { props: ['show', 'title'], template: '<div v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>' }, Icon: true, Pagination: true
} } })
const button = (wrapper: ReturnType<typeof setup>, text: string) => wrapper.findAll('button').find(item => item.text().includes(text))!

describe('UserCleanupControl', () => {
  beforeEach(() => { vi.clearAllMocks(); summary.mockResolvedValue(snapshot()); preview.mockResolvedValue(snapshot()); execute.mockResolvedValue({ deleted_count: 2, zero_balance: 1, negative_balance: 1, skipped_count: 0 }); updateGuard.mockResolvedValue({}) })

  it('only previews on opening and requires a second explicit confirmation', async () => {
    const wrapper = setup(); await flushPromises()
    expect(wrapper.text()).toContain('可清理 2 人')
    await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    expect(execute).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="cleanup-execute"]').attributes('disabled')).toBeDefined()
    await button(wrapper, '查看用户').trigger('click')
    expect(wrapper.text()).toContain('negative@example.test'); expect(wrapper.text()).toContain('-0.00000001')
    await wrapper.get('[data-testid="cleanup-confirm"]').setValue(true)
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click'); await flushPromises()
    expect(execute).toHaveBeenCalledWith(snapshot()); expect(wrapper.emitted('completed')).toHaveLength(1)
    expect(wrapper.text()).toContain('已清理 2 个用户'); expect(wrapper.text()).toContain('0 余额 1 人，负余额 1 人')
  })

  it('shows structured expiry errors and requires a fresh preview instead of retrying deletion', async () => {
    execute.mockRejectedValueOnce({ code: 'USER_CLEANUP_PREVIEW_EXPIRED', message: '清理预览已过期，请重新预览' })
    const wrapper = setup(); await flushPromises(); await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="cleanup-confirm"]').setValue(true)
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('清理预览已过期，请重新预览')
    expect(wrapper.get('[data-testid="cleanup-execute"]').attributes('disabled')).toBeDefined()
    await button(wrapper, '重新预览').trigger('click'); await flushPromises()
    expect(preview).toHaveBeenCalledTimes(2)
    expect((wrapper.get('[data-testid="cleanup-confirm"]').element as HTMLInputElement).checked).toBe(false)
    wrapper.unmount()
  })

  it('cancels without deletion and never submits twice while pending', async () => {
    const wrapper = setup(); await flushPromises(); await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    await button(wrapper, '取消').trigger('click'); expect(execute).not.toHaveBeenCalled()
    await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    let resolve!: (value: unknown) => void
    execute.mockReturnValue(new Promise(done => { resolve = done }))
    await wrapper.get('[data-testid="cleanup-confirm"]').setValue(true)
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click')
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click')
    expect(execute).toHaveBeenCalledTimes(1)
    resolve({ deleted_count: 1, zero_balance: 1, negative_balance: 0, skipped_count: 1 }); await flushPromises()
    expect(wrapper.text()).toContain('1 人在预览后发生变化')
  })

  it('keeps the same immutable preview on a network error for safe retry', async () => {
    execute.mockRejectedValueOnce(new Error('连接中断，请重试'))
    const wrapper = setup(); await flushPromises(); await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="cleanup-confirm"]').setValue(true)
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('连接中断')
    await wrapper.get('[data-testid="cleanup-execute"]').trigger('click'); await flushPromises()
    expect(execute).toHaveBeenNthCalledWith(1, snapshot()); expect(execute).toHaveBeenNthCalledWith(2, snapshot())
    expect(preview).toHaveBeenCalledTimes(1)
  })

  it('re-previews and clears confirmation after retaining a candidate', async () => {
    const wrapper = setup(); await flushPromises(); await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="cleanup-confirm"]').setValue(true); await button(wrapper, '查看用户').trigger('click')
    preview.mockResolvedValueOnce({ ...snapshot(), total: 1, candidates: [snapshot().candidates[1]] })
    await button(wrapper, '保留此用户').trigger('click'); await flushPromises()
    expect(updateGuard).toHaveBeenCalledWith(7, true); expect(preview).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="cleanup-execute"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('old@example.test')
  })

  it('does not offer execution when preview cannot be obtained', async () => {
    preview.mockRejectedValue(new Error('无法读取清理条件'))
    const wrapper = setup(); await flushPromises(); await button(wrapper, '清理无余额用户').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('无法读取清理条件'); expect(wrapper.get('[data-testid="cleanup-execute"]').attributes('disabled')).toBeDefined()
    expect(execute).not.toHaveBeenCalled()
  })
})
