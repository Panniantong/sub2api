import { describe, expect, it } from 'vitest'
import type { AnnouncementCondition, AnnouncementTargeting } from '@/types'
import {
  hasEmptyUserCondition,
  isSpecificUsersTargeting,
  onlyTargetsSpecificUsers,
  targetingUserIds
} from '../announcementTargeting'

const users = (...ids: number[]): AnnouncementCondition => ({ type: 'user', operator: 'in', user_ids: ids })
const balance: AnnouncementCondition = { type: 'balance', operator: 'lt', value: 5 }
const rule = (...groups: AnnouncementCondition[][]): AnnouncementTargeting => ({
  any_of: groups.map((all_of) => ({ all_of }))
})

describe('announcementTargeting', () => {
  it('recognizes the single specific-users rule used by the editor', () => {
    expect(isSpecificUsersTargeting(rule([users(7)]))).toBe(true)
    expect(isSpecificUsersTargeting(rule([users(7), balance]))).toBe(false)
    expect(isSpecificUsersTargeting(rule([users(7)], [users(9)]))).toBe(false)
    expect(isSpecificUsersTargeting({ any_of: [] })).toBe(false)
    expect(isSpecificUsersTargeting(undefined)).toBe(false)
  })

  it('tells whether a rule only names users and collects them without duplicates', () => {
    expect(onlyTargetsSpecificUsers(rule([users(7, 9)], [users(9, 11)]))).toBe(true)
    expect(onlyTargetsSpecificUsers(rule([users(7), balance]))).toBe(false)
    expect(onlyTargetsSpecificUsers({ any_of: [] })).toBe(false)
    expect(targetingUserIds(rule([users(7, 9), balance], [users(9, 11)]))).toEqual([7, 9, 11])
  })

  it('flags user conditions that have no user yet', () => {
    expect(hasEmptyUserCondition(rule([users()]))).toBe(true)
    expect(hasEmptyUserCondition(rule([{ type: 'user', operator: 'in' }]))).toBe(true)
    expect(hasEmptyUserCondition(rule([users(7), balance]))).toBe(false)
    expect(hasEmptyUserCondition({ any_of: [] })).toBe(false)
  })
})
