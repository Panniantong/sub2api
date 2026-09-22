import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import HarvestFlowView from '../HarvestFlowView.vue'

const { getFlow, updateSkip } = vi.hoisted(() => ({ getFlow: vi.fn(), updateSkip: vi.fn() }))

vi.mock('@/api/admin/accounts', () => ({
  getCodexHarvestFlow: getFlow,
  updateCodexSkipHarvest: updateSkip
}))
vi.mock('@/components/admin/HarvestControlsPanel.vue', () => ({ default: { template: '<section />' } }))
vi.mock('@/components/admin/HarvestManualConsole.vue', () => ({ default: { template: '<section />' } }))
vi.mock('@/components/admin/HarvestNodeRecords.vue', () => ({ default: { template: '<section />' } }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      if (!params) return key
      return `${key} ${Object.values(params).map(value => String(value)).join(' ')}`.trim()
    }
  })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))

function response() {
  return {
    harvest: { enabled: false, fail_closed: false, strategy: 'standby', models: [] },
    sidecar: { reachable: false },
    stages: [],
    accounts: [{
      id: 1,
      name: 'Test account',
      status: 'active',
      schedulable: true,
      skip_harvest: false,
      in_scope: true,
      ready_count: 0,
      tickets: null
    }],
    counts: {
      tickets_ready: 0, tickets_blocked: 0, probe_hit: 0, probe_miss: 0,
      ticket_accept: 0, ticket_reject: 0, select_ok: 0, select_skip: 0, select_fail: 0
    },
    events: []
  }
}

describe('HarvestFlowView nullable API lists', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    getFlow.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it.each([
    {
      name: 'disabled harvesting returns null tickets',
      payload: response(),
      text: '0/0'
    },
    {
      name: 'no eligible accounts returns null accounts',
      payload: { ...response(), accounts: null },
      text: 'admin.harvestFlow.noAccounts'
    },
    {
      name: 'null events still render the empty state',
      payload: { ...response(), accounts: [], events: null },
      text: 'admin.harvestFlow.noEvents'
    },
    {
      name: 'normal ticket lists still render',
      payload: {
        ...response(),
        accounts: [{
          ...response().accounts[0],
          tickets: [{ model: 'gpt-5.6-sol', ready: false, blocked: false, remaining_seconds: 0 }]
        }]
      },
      text: 'gpt-5.6-sol'
    }
  ])('$name', async ({ payload, text }) => {
    getFlow.mockResolvedValue(payload)
    const errors: unknown[] = []
    const wrapper = mount(HarvestFlowView, {
      global: {
        stubs: { Icon: true, LoadingSpinner: true },
        config: { errorHandler: (error) => { errors.push(error) } }
      }
    })
    try {
      await flushPromises()
      expect(errors).toEqual([])
      expect(wrapper.text()).toContain('admin.harvestFlow.title')
      expect(wrapper.text()).toContain(text)
    } finally {
      wrapper.unmount()
    }
  })
})

describe('HarvestFlowView external proxy', () => {
  it('shows external proxy mode without a sidecar error or node pool', async () => {
    getFlow.mockResolvedValue({ ...response(), sidecar: { mode: 'external', reachable: false }, stages: [{ id: 'node', status: 'idle', detail: 'external_proxy' }] })
    const wrapper = mount(HarvestFlowView, { global: { stubs: { Icon: true, LoadingSpinner: true } } })
    try {
      await flushPromises()
      expect(wrapper.text()).toContain('admin.harvestFlow.externalProxy')
      expect(wrapper.text()).toContain('admin.harvestFlow.externalProxyHint')
      expect(wrapper.text()).not.toContain('admin.harvestFlow.sidecarOffline')
      expect(wrapper.text()).not.toContain('admin.harvestFlow.waitingSidecar')
      expect(wrapper.text()).not.toContain('CODEX-ROTATE')
    } finally { wrapper.unmount() }
  })
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function mountFlow() {
  return mount(HarvestFlowView, { global: { stubs: { Icon: true, LoadingSpinner: true } } })
}

function button(wrapper: ReturnType<typeof mountFlow>, label: string) {
  const found = wrapper.findAll('button').find(item => item.text() === label)
  if (!found) throw new Error(`Button not found: ${label}`)
  return found
}

describe('HarvestFlowView request ordering', () => {
  let wrapper: ReturnType<typeof mountFlow> | undefined
  beforeEach(() => {
    vi.useFakeTimers()
    getFlow.mockReset()
    updateSkip.mockReset()
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it.each(['resolve', 'reject'])('ignores a stale refresh %s after a newer response', async completion => {
    const old = deferred<ReturnType<typeof response>>()
    const fresh = response()
    fresh.accounts[0]!.name = 'Newest snapshot'
    getFlow.mockResolvedValueOnce(response()).mockReturnValueOnce(old.promise).mockResolvedValueOnce(fresh)
    wrapper = mountFlow()
    await flushPromises()
    await button(wrapper, 'common.refresh').trigger('click')
    await button(wrapper, 'common.refresh').trigger('click')
    await flushPromises()
    if (completion === 'resolve') old.resolve(response())
    else old.reject(new Error('obsolete error'))
    await flushPromises()
    expect(wrapper.text()).toContain('Newest snapshot')
    expect(wrapper.text()).not.toContain('obsolete error')
  })

  it('does not overlap automatic refresh requests', async () => {
    const pending = deferred<ReturnType<typeof response>>()
    getFlow.mockReturnValueOnce(pending.promise).mockResolvedValue(response())
    wrapper = mountFlow()
    await vi.advanceTimersByTimeAsync(15000)
    expect(getFlow).toHaveBeenCalledTimes(1)
    pending.resolve(response())
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(getFlow).toHaveBeenCalledTimes(2)
  })

  it('isolates reads during a save and preserves the saved toggle when refresh fails', async () => {
    const old = deferred<ReturnType<typeof response>>()
    const save = deferred<{ account_id: number; skip_harvest: boolean }>()
    getFlow.mockResolvedValueOnce(response()).mockReturnValueOnce(old.promise)
      .mockRejectedValueOnce(new Error('refresh failed'))
    updateSkip.mockReturnValue(save.promise)
    wrapper = mountFlow()
    await flushPromises()
    await button(wrapper, 'common.refresh').trigger('click')
    await button(wrapper, 'admin.harvestFlow.skipHarvest').trigger('click')
    await vi.advanceTimersByTimeAsync(10000)
    expect(getFlow).toHaveBeenCalledTimes(2)
    save.resolve({ account_id: 1, skip_harvest: true })
    await flushPromises()
    expect(updateSkip).toHaveBeenCalledWith(1, true)
    expect(wrapper.text()).toContain('admin.harvestFlow.enableHarvest')
    expect(wrapper.text()).toContain('refresh failed')
    old.resolve(response())
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.enableHarvest')
    expect(wrapper.text()).toContain('refresh failed')
  })

  it('keeps the old toggle and reports failed saves', async () => {
    getFlow.mockResolvedValue(response())
    updateSkip.mockRejectedValue(new Error('save failed'))
    wrapper = mountFlow()
    await flushPromises()
    await button(wrapper, 'admin.harvestFlow.skipHarvest').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('save failed')
    expect(button(wrapper, 'admin.harvestFlow.skipHarvest').attributes('disabled')).toBeUndefined()
    expect(getFlow).toHaveBeenCalledTimes(1)
  })

  it('does not refresh after a save completes on an unmounted page', async () => {
    const save = deferred<{ account_id: number; skip_harvest: boolean }>()
    getFlow.mockResolvedValue(response())
    updateSkip.mockReturnValue(save.promise)
    wrapper = mountFlow()
    await flushPromises()
    await button(wrapper, 'admin.harvestFlow.skipHarvest').trigger('click')
    wrapper.unmount()
    wrapper = undefined
    save.resolve({ account_id: 1, skip_harvest: true })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(10000)
    expect(getFlow).toHaveBeenCalledTimes(1)
  })

  it('switches the events pane to node learning', async () => {
    getFlow.mockResolvedValue(response())
    wrapper = mountFlow()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.nodes.title')
    expect(wrapper.text()).toContain('admin.harvestFlow.noEvents')
    await button(wrapper, 'admin.harvestFlow.nodes.title').trigger('click')
    expect(wrapper.text()).not.toContain('admin.harvestFlow.noEvents')
  })

  it('shows scope errors and blocked leftover tickets instead of a ready badge', async () => {
    const data = response()
    getFlow.mockResolvedValue({
      ...data,
      harvest: { ...data.harvest, scope_mode: 'selected', scope_error: true },
      accounts: [{ ...data.accounts[0], in_scope: false, tickets: [
        { model: 'gpt-6-astra', ready: true, blocked: true, remaining_seconds: 600 }
      ] }]
    })
    wrapper = mountFlow()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.scopeUnavailable')
    expect(wrapper.text()).toContain('admin.harvestFlow.reasons.harvest_excluded')
    expect(wrapper.text()).not.toContain('admin.harvestFlow.remaining')
  })
})

describe('HarvestFlowView account cards', () => {
  let wrapper: ReturnType<typeof mountFlow> | undefined
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-20T23:13:00+08:00'))
    getFlow.mockReset()
    updateSkip.mockReset()
  })
  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  function accountsPayload() {
    const data = response()
    return {
      ...data,
      accounts: [
        {
          id: 2,
          name: '20x',
          status: 'active',
          schedulable: true,
          skip_harvest: false,
          in_scope: true,
          ready_count: 2,
          tickets: [
            {
              model: 'gpt-6-astra',
              ready: true,
              blocked: false,
              remaining_seconds: 2880,
              length: 292,
              expires_at: '2026-09-20T23:52:21+08:00',
              standby: true,
              probe: { result: 'success', http_status: 200, checked_at: '2026-09-20T22:52:53+08:00' }
            },
            {
              model: 'gpt-5.6-sol',
              ready: true,
              blocked: false,
              remaining_seconds: 2940,
              length: 292,
              expires_at: '2026-09-20T23:52:45+08:00',
              standby_expires_at: '2026-09-21T00:40:00+08:00',
              probe: { result: 'success', http_status: 200, checked_at: '2026-09-20T22:53:17+08:00' }
            }
          ]
        },
        {
          id: 3,
          name: '5x',
          status: 'active',
          schedulable: true,
          skip_harvest: true,
          in_scope: false,
          ready_count: 0,
          tickets: [
            {
              model: 'gpt-6-astra',
              ready: false,
              blocked: false,
              remaining_seconds: 0,
              probe: { result: 'success', http_status: 200, checked_at: '2026-09-20T13:12:07+08:00' }
            },
            {
              model: 'gpt-5.6-sol',
              ready: false,
              blocked: false,
              remaining_seconds: 0,
              probe: { result: 'success', http_status: 200, checked_at: '2026-09-20T13:12:06+08:00' }
            }
          ]
        }
      ]
    }
  }

  it('aligns harvest and skip-harvest cards and labels standby remaining', async () => {
    getFlow.mockResolvedValue(accountsPayload())
    wrapper = mountFlow()
    await flushPromises()
    const cards = wrapper.findAll('[data-test="harvest-account-card"]')
    expect(cards).toHaveLength(2)
    expect(wrapper.findAll('[data-test="harvest-account-hint"]')).toHaveLength(2)
    expect(wrapper.findAll('[data-test="harvest-ticket-chip"]')).toHaveLength(4)
    expect(cards[0]!.classes()).toEqual(cards[1]!.classes())
    expect(wrapper.text()).toContain('admin.harvestFlow.remainingStandby')
    expect(wrapper.text()).toContain('admin.harvestFlow.remainingWithStandby')
    expect(cards[0]!.text()).toContain('success · HTTP 200')
    expect(cards[1]!.text()).toContain('admin.harvestFlow.lastProbe')
    expect(cards[1]!.text()).not.toContain('success · HTTP 200')
    expect(cards[0]!.text()).toContain('292B')
    expect(cards[1]!.text()).toContain('—')
    expect(cards[1]!.text()).toContain('admin.harvestFlow.skipHarvestHint')
    expect(cards[0]!.text()).not.toContain('admin.harvestFlow.skipHarvestHint')
    expect(cards[0]!.text()).toContain('admin.harvestFlow.availability.available')
    expect(cards[1]!.text()).toContain('admin.harvestFlow.availability.available')
  })

  it('counts remaining from expires_at instead of a stale snapshot second', async () => {
    const data = accountsPayload()
    data.accounts[0]!.tickets[0]!.remaining_seconds = 10
    getFlow.mockResolvedValue(data)
    wrapper = mountFlow()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.harvestFlow.durationMinutes')
    expect(wrapper.text()).toContain('39')
    expect(wrapper.text()).not.toContain('admin.harvestFlow.durationSeconds')
    expect(wrapper.text()).not.toContain('10s')
  })
})