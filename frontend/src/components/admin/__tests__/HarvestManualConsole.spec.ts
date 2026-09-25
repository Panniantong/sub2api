import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import HarvestManualConsole from '@/components/admin/HarvestManualConsole.vue'
import type { CodexHarvestFlowAccount, ManualHarvestProgress } from '@/api/admin/accounts'

const stream = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/accounts', () => ({ streamManualCodexHarvest: stream }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (!params) return key
      return `${key} ${Object.values(params).map(value => String(value)).join(' ')}`.trim()
    }
  })
}))

function account(partial: Partial<CodexHarvestFlowAccount> = {}): CodexHarvestFlowAccount {
  return {
    id: 2,
    name: '20x',
    status: 'active',
    schedulable: true,
    availability: 'available',
    tickets: [],
    ready_count: 0,
    blocked_count: 0,
    ...partial
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

let wrapper: VueWrapper | undefined
beforeEach(() => {
  stream.mockReset()
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('HarvestManualConsole', () => {
  it('streams a directed harvest with the selected switch rule', async () => {
    const pending = deferred<void>()
    let onProgress: ((progress: ManualHarvestProgress) => void) | undefined
    stream.mockImplementation((_id, _body, progress) => {
      onProgress = progress
      return pending.promise
    })
    wrapper = mount(HarvestManualConsole, {
      props: { accounts: [account(), account({ id: 3, name: '5x' })], models: ['gpt-6-astra', 'gpt-5.4-sol'] },
      attachTo: document.body
    })
    await wrapper.get('[data-testid="manual-account-input"]').trigger('focus')
    await wrapper.get('[data-testid="manual-account-2"]').trigger('click')
    await wrapper.get('[data-testid="manual-node-switch"]').setValue('every_request')
    await wrapper.get('[data-testid="manual-start"]').trigger('click')
    await flushPromises()
    expect(stream).toHaveBeenCalledWith(2, expect.objectContaining({
      node_switch_rule: 'every_request',
      collect_lanes: 1,
      stop_on_success: true,
      models: ['gpt-6-astra', 'gpt-5.4-sol']
    }), expect.any(Function), expect.any(AbortSignal))
    onProgress?.({ attempt: 1, max_attempts: 20, node: 'Known node', result: 'hit', level: 'OK', message: '合格票', tickets_stored: 1, done: true })
    pending.resolve()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.console.success')
    expect(wrapper.get('[data-testid="manual-logs"]').text()).toContain('合格票')
    expect(wrapper.emitted('finished')).toHaveLength(1)
  })

  it('starts parallel collection with a bounded lane count and supports cancellation', async () => {
    const pending = deferred<void>()
    let activeSignal: AbortSignal | undefined
    stream.mockImplementation((_id, _body, _progress, signal: AbortSignal) => {
      activeSignal = signal
      signal.addEventListener('abort', () => pending.reject(Object.assign(new Error('aborted'), { name: 'AbortError' })))
      return pending.promise
    })
    wrapper = mount(HarvestManualConsole, {
      props: { accounts: [account()], models: ['gpt-6-astra'] }
    })
    await wrapper.get('[data-testid="manual-account-input"]').trigger('focus')
    await wrapper.get('[data-testid="manual-account-2"]').trigger('click')
    await wrapper.get('[data-testid="manual-collect-lanes"]').setValue(4)
    await wrapper.get('[data-testid="manual-parallel-start"]').trigger('click')
    await flushPromises()
    expect(stream).toHaveBeenCalledWith(2, expect.objectContaining({ collect_lanes: 4, max_attempts: 20 }), expect.any(Function), expect.any(AbortSignal))
    expect(wrapper.find('[data-testid="manual-start"]').exists()).toBe(false)
    await wrapper.get('[data-testid="manual-stop"]').trigger('click')
    await flushPromises()
    expect(activeSignal?.aborted).toBe(true)
  })

  it('aborts the in-flight harvest when stopped', async () => {
    const pending = deferred<void>()
    stream.mockImplementation((_id, _body, _progress, signal: AbortSignal) => {
      signal.addEventListener('abort', () => pending.reject(Object.assign(new Error('aborted'), { name: 'AbortError' })))
      return pending.promise
    })
    wrapper = mount(HarvestManualConsole, {
      props: { accounts: [account()], models: ['gpt-6-astra'] },
      attachTo: document.body
    })
    await wrapper.get('[data-testid="manual-account-input"]').trigger('focus')
    await wrapper.get('[data-testid="manual-account-2"]').trigger('click')
    await wrapper.get('[data-testid="manual-start"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="manual-stop"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.console.stopped')
    expect(wrapper.text()).toContain('admin.harvestFlow.console.stopLog')
    expect(wrapper.emitted('finished')).toBeUndefined()
  })
})
