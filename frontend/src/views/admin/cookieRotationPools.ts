import type { AccountListItem } from '@/types'
import type { CookieRotationPoolPolicy } from '@/api/admin/cookies'

export const defaultCookieRotationPool = (groupID: number): CookieRotationPoolPolicy => ({ group_id: groupID, group_ids: [groupID], enabled: true, demand_mode: 'dynamic', scale_down_seconds: 60, target_concurrency: 70, redundancy_percent: 50, min_accounts: 1, max_accounts: 0, ready_timeout_seconds: 30, retry_seconds: 60, preparation_multiplier: 2, max_preparing_accounts: 0, low_host_rotation_enabled: false, low_host_threshold: 5, low_host_account_slots: 10 })
export const cookiePoolGroupIDs = (p: CookieRotationPoolPolicy) => p.group_ids?.length ? p.group_ids : (p.group_id > 0 ? [p.group_id] : [])
export const cookiePoolTarget = (p: CookieRotationPoolPolicy, demand = 0) => {
  const base = p.demand_mode === 'dynamic' ? demand : p.target_concurrency
  return p.redundancy_mode === 'fixed' ? base + (p.redundancy_concurrency || 0) : Math.ceil(base * (100 + p.redundancy_percent) / 100)
}
export function cookiePoolPreview(p: CookieRotationPoolPolicy, accounts: AccountListItem[], demand = 0, groupID = p.group_id) {
  let capacity = 0, count = 0
  for (const a of [...accounts].sort((a, b) => a.id - b.id)) {
    if (!a.group_ids?.includes(groupID) || a.status !== 'active' || !a.schedulable || a.concurrency <= 0) continue
    if ((p.max_accounts > 0 && count >= p.max_accounts) || (count >= p.min_accounts && capacity >= cookiePoolTarget(p, demand))) break
    capacity += a.concurrency
    count++
  }
  return { count, capacity }
}
