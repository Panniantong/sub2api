import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { reactive } from 'vue'
import { useAccountQualityStore } from '../accountQuality'
import { listQualityPlans, listQualityOperations } from '@/api/admin/accountQuality'
import { getAllIncludingInactive } from '@/api/admin/groups'
const state = vi.hoisted(() => ({ auth: null as any }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('@/api/admin/accountQuality', () => ({ listQualityPlans: vi.fn(), listQualityOperations: vi.fn() }))
vi.mock('@/api/admin/groups', () => ({ getAllIncludingInactive: vi.fn() }))
const deferred = <T>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(yes => { resolve = yes }); return { promise, resolve } }
beforeEach(() => { setActivePinia(createPinia()); state.auth = reactive({ user: { id: 1, role: 'admin' } }); vi.resetAllMocks(); vi.mocked(listQualityPlans).mockResolvedValue([]); vi.mocked(listQualityOperations).mockResolvedValue({ items: [], next_cursor: 0 }); vi.mocked(getAllIncludingInactive).mockResolvedValue([]) })
describe('quality workspace session state', () => {
  it('loads independent panels concurrently and retains visible rules while refreshing', async () => {
    const first = deferred<any[]>(); vi.mocked(listQualityPlans).mockReturnValueOnce(first.promise)
    const store = useAccountQualityStore(); const loading = store.refresh()
    expect(listQualityOperations).toHaveBeenCalled(); expect(getAllIncludingInactive).toHaveBeenCalled()
    first.resolve([{ id: 1, account_id: 12, account_name: 'named account' }]); await loading
    store.search = 'named'; const next = deferred<any[]>(); vi.mocked(listQualityPlans).mockReturnValueOnce(next.promise)
    const refresh = store.refreshRules(); expect(store.plans[0].account_name).toBe('named account')
    expect(useAccountQualityStore().search).toBe('named')
    next.resolve([]); await refresh
  })
  it('discards a stale pagination response after a newer refresh', async () => {
    const store = useAccountQualityStore(); store.operations = [{ id: 10 }] as any; store.cursor = 10
    const append = deferred<any>(); vi.mocked(listQualityOperations).mockReturnValueOnce(append.promise).mockResolvedValueOnce({ items: [{ id: 20 }] as any, next_cursor: 0 })
    const pending = store.refreshOperations(true); await store.refreshOperations()
    append.resolve({ items: [{ id: 9 }], next_cursor: 9 }); await pending
    expect(store.operations.map(o => o.id)).toEqual([20]); expect(store.cursor).toBe(0)
  })
  it('clears cached data on logout and rejects an old session response', async () => {
    const store = useAccountQualityStore(); store.plans = [{ id: 1 }] as any
    const request = deferred<any[]>(); vi.mocked(listQualityPlans).mockReturnValueOnce(request.promise)
    const pending = store.refreshRules(); state.auth.user = null
    expect(store.plans).toEqual([]); request.resolve([{ id: 99 }]); await pending
    expect(store.plans).toEqual([]); expect(store.rulesLoaded).toBe(false)
  })
  it('does not clear snapshots when the same user profile is refreshed', () => {
    const store = useAccountQualityStore(); store.plans = [{ id: 1 }] as any
    state.auth.user = { id: 1, role: 'admin', balance: 100 }
    expect(store.plans).toHaveLength(1)
  })
})
