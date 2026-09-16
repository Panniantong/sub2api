import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))

import { applyAntiDegrade, listAntiDegradeStrategies, previewAntiDegrade, revertAntiDegrade } from '@/api/admin/accounts'

describe('account protection API', () => {
  beforeEach(() => vi.resetAllMocks())

  it.each(['mode1', 'mode2', 'legacy'] as const)('previews and applies %s via the mode query', async mode => {
    const preview = { account_id: 42, enabled: false, eligible: true, active_mode: '', policy_version: 0, identity_ready: false, tls_profile: '', issues: [], changes: [] }
    get.mockResolvedValue({ data: preview })
    expect(await previewAntiDegrade(42, mode)).toEqual(preview)
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/anti-degrade', { params: { mode } })

    const account = { id: 42, extra: { anti_degrade: { enabled: false } } }
    post.mockResolvedValue({ data: account })
    expect(await applyAntiDegrade(42, mode)).toEqual(account)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/anti-degrade/apply', null, { params: { mode } })
  })

  it('defaults to legacy and leaves revert response unchanged', async () => {
    get.mockResolvedValue({ data: {} })
    post.mockResolvedValue({ data: { id: 42, extra: {} } })
    await previewAntiDegrade(42)
    await applyAntiDegrade(42)
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/anti-degrade', { params: { mode: 'legacy' } })
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/anti-degrade/apply', null, { params: { mode: 'legacy' } })
    expect(await revertAntiDegrade(42)).toEqual({ id: 42, extra: {} })
    expect(post).toHaveBeenLastCalledWith('/admin/accounts/42/anti-degrade/revert')
  })

  it('loads the server-owned strategy registry', async () => {
    const strategies = [{ id: 'minimal_compat', name: '最小兼容' }]
    get.mockResolvedValue({ data: { strategies } })
    await expect(listAntiDegradeStrategies()).resolves.toEqual(strategies)
    expect(get).toHaveBeenCalledWith('/admin/accounts/anti-degrade/strategies')
  })
})
