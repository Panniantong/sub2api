import type { AnnouncementCondition, AnnouncementTargeting } from '@/types'

const allConditions = (targeting?: AnnouncementTargeting | null): AnnouncementCondition[] =>
  (targeting?.any_of ?? []).flatMap((group) => group?.all_of ?? [])

/** 规则恰好是「一个条件组里只有一个指定用户条件」，即编辑器里的「指定用户」模式。 */
export function isSpecificUsersTargeting(targeting?: AnnouncementTargeting | null): boolean {
  const groups = targeting?.any_of ?? []
  return groups.length === 1 && groups[0]?.all_of?.length === 1 && groups[0].all_of[0]?.type === 'user'
}

/** 规则里全部是指定用户条件（没有套餐、余额等其他条件）。 */
export function onlyTargetsSpecificUsers(targeting?: AnnouncementTargeting | null): boolean {
  const conditions = allConditions(targeting)
  return conditions.length > 0 && conditions.every((cond) => cond.type === 'user')
}

/** 规则里所有指定用户条件涉及的用户 ID，按出现顺序去重。 */
export function targetingUserIds(targeting?: AnnouncementTargeting | null): number[] {
  const ids = allConditions(targeting)
    .filter((cond) => cond.type === 'user')
    .flatMap((cond) => cond.user_ids ?? [])
  return [...new Set(ids)]
}

/** 是否有指定用户条件还没选用户（后端会拒绝这样的规则）。 */
export function hasEmptyUserCondition(targeting?: AnnouncementTargeting | null): boolean {
  return allConditions(targeting).some((cond) => cond.type === 'user' && !cond.user_ids?.length)
}
