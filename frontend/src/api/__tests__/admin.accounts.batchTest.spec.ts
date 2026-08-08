import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, post } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, post }
}))

import { createBatchTest, getBatchTest } from '@/api/admin/accounts'

describe('admin account batch test API', () => {
  beforeEach(() => {
    get.mockReset()
    post.mockReset()
  })

  it('creates and polls an in-memory batch test job', async () => {
    const job = {
      job_id: 'job/1',
      state: 'running',
      model: 'gpt-5.4-mini',
      total: 3,
      processed: 1,
      success: 1,
      failed: 0,
      created_at: '2026-08-08T00:00:00Z'
    }
    post.mockResolvedValueOnce({ data: job })
    get.mockResolvedValueOnce({ data: job })

    await expect(createBatchTest([1, 2, 3])).resolves.toEqual(job)
    await expect(getBatchTest('job/1')).resolves.toEqual(job)

    expect(post).toHaveBeenCalledWith('/admin/accounts/batch-test', { account_ids: [1, 2, 3] })
    expect(get).toHaveBeenCalledWith('/admin/accounts/batch-test/job%2F1')
  })
})
