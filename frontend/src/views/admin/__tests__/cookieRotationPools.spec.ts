import { describe, expect, it } from 'vitest'
import type { AccountListItem } from '@/types'
import { cookiePoolPreview, cookiePoolTarget, defaultCookieRotationPool } from '../cookieRotationPools'

describe('Cookie rotation pool capacity', () => {
  it('keeps both reserve values and applies only the selected mode', () => {
    const p = { ...defaultCookieRotationPool(2), redundancy_concurrency: 35, redundancy_mode: 'fixed' as 'fixed' | 'percent' }
    expect(cookiePoolTarget(p, 70)).toBe(105)
    expect(cookiePoolTarget(p, 100)).toBe(135)
    expect(cookiePoolTarget(p, 0)).toBe(35)
    p.redundancy_mode = 'percent'
    expect(cookiePoolTarget(p, 100)).toBe(150)
    expect(p.redundancy_concurrency).toBe(35)
    p.redundancy_mode = 'fixed'
    p.demand_mode = 'fixed'
    expect(cookiePoolTarget(p, 100)).toBe(105)
    const accounts = Array.from({ length: 10 }, (_, i) => ({ id: i + 1, group_ids: [2], status: 'active', schedulable: true, concurrency: 35 }) as AccountListItem)
    expect(cookiePoolPreview(p, accounts)).toEqual({ count: 3, capacity: 105 })
    p.max_accounts = 2
    expect(cookiePoolPreview(p, accounts)).toEqual({ count: 2, capacity: 70 })
  })
  it('prepares three 35-concurrency accounts for 70 demand with 50 percent redundancy', () => {
    const p = defaultCookieRotationPool(2)
    p.demand_mode = 'fixed'
    const accounts = Array.from({ length: 10 }, (_, i) => ({ id: i + 1, group_ids: [2], schedulable: true, status: 'active', concurrency: 35 }) as AccountListItem)
    expect(cookiePoolTarget(p)).toBe(105)
    expect(cookiePoolPreview(p, accounts)).toEqual({ count: 3, capacity: 105 })
    accounts[0]!.schedulable = false
    accounts[1]!.group_ids = [3]
    p.max_accounts = 2
    expect(cookiePoolPreview(p, accounts)).toEqual({ count: 2, capacity: 70 })
  })
  it('adds actual capacities instead of treating different accounts as equal', () => {
    const accounts = [60, 20, 30, 35].map((concurrency, i) => ({ id: i + 1, group_ids: [2], schedulable: true, status: 'active', concurrency }) as AccountListItem)
    expect(cookiePoolPreview({ ...defaultCookieRotationPool(2), demand_mode: 'fixed' }, accounts)).toEqual({ count: 3, capacity: 110 })
  })
  it('scales from warm minimum to all available accounts without a cap', () => {
    const p = defaultCookieRotationPool(2)
    const accounts = Array.from({ length: 15 }, (_, i) => ({ id: i + 1, group_ids: [2], status: 'active', schedulable: true, concurrency: 35 }) as AccountListItem)
    expect(cookiePoolPreview(p, accounts, 0)).toEqual({ count: 1, capacity: 35 })
    expect(cookiePoolPreview(p, accounts, 70)).toEqual({ count: 3, capacity: 105 })
    expect(cookiePoolPreview(p, accounts, 350)).toEqual({ count: 15, capacity: 525 })
    expect(cookiePoolPreview(p, accounts, 70, 3)).toEqual({ count: 0, capacity: 0 })
  })
})
