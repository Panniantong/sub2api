export const HARVEST_AVAILABILITY_ORDER = [
  'available',
  'rate_limited',
  'overload',
  'temp_unschedulable',
  'error',
  'disabled',
  'expired'
] as const

export type HarvestAvailability = typeof HARVEST_AVAILABILITY_ORDER[number]

export function isHarvestAvailability(value?: string): value is HarvestAvailability {
  return !!value && (HARVEST_AVAILABILITY_ORDER as readonly string[]).includes(value)
}

export function resolveHarvestAvailability(account: {
  availability?: string
  status?: string
  schedulable?: boolean
}): HarvestAvailability {
  if (isHarvestAvailability(account.availability)) return account.availability
  if (account.status && account.status !== 'active') return 'error'
  if (!account.schedulable) return 'disabled'
  return 'available'
}

export function harvestAvailabilityRank(kind: string): number {
  const index = HARVEST_AVAILABILITY_ORDER.indexOf(kind as HarvestAvailability)
  return index < 0 ? HARVEST_AVAILABILITY_ORDER.length : index
}

export function harvestAvailabilityClass(kind: string): string {
  switch (kind) {
    case 'available':
      return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300'
    case 'rate_limited':
    case 'overload':
    case 'temp_unschedulable':
      return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-300'
    case 'error':
      return 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-800 dark:bg-rose-950/40 dark:text-rose-300'
    default:
      return 'border-gray-200 bg-gray-100 text-gray-500 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-400'
  }
}

export function harvestAvailabilityRecoverClock(recoverAt?: string | null, now = new Date()): { clock: string; date?: string } | null {
  if (!recoverAt) return null
  const target = new Date(recoverAt)
  if (Number.isNaN(target.getTime())) return null
  const pad = (n: number) => String(n).padStart(2, '0')
  const clock = `${pad(target.getHours())}:${pad(target.getMinutes())}`
  if (target.toDateString() === now.toDateString()) return { clock }
  return { clock, date: `${pad(target.getMonth() + 1)}-${pad(target.getDate())}` }
}

export function harvestAccountLabel(account: { id: number; name?: string }): string {
  return `#${account.id} · ${account.name || 'Account'}`
}

export function harvestAccountMatchScore(account: { id: number; name?: string }, query: string): number {
  if (!query) return 0
  const idText = `#${account.id}`
  const name = (account.name || '').toLowerCase()
  const label = harvestAccountLabel(account).toLowerCase()
  if (idText === query || String(account.id) === query) return 1000
  if (label.startsWith(query)) return 900
  if (name && name.startsWith(query)) return 800
  if (label.includes(query)) return 600
  if (name && name.includes(query)) return 500
  let matched = 0
  for (const ch of label) {
    if (ch === query[matched]) matched += 1
    if (matched >= query.length) break
  }
  return matched >= query.length ? 300 : 0
}

export function orderHarvestAccounts<T extends { id: number; name?: string; availability?: string; status?: string; schedulable?: boolean; recover_at?: string }>(
  accounts: T[],
  keyword: string,
  selected?: T | null
): T[] {
  const raw = keyword.trim()
  const query = selected && raw === harvestAccountLabel(selected) ? '' : raw.toLowerCase()
  return [...accounts].sort((a, b) => {
    if (query) {
      const score = harvestAccountMatchScore(b, query) - harvestAccountMatchScore(a, query)
      if (score !== 0) return score
    }
    const rank = harvestAvailabilityRank(resolveHarvestAvailability(a)) - harvestAvailabilityRank(resolveHarvestAvailability(b))
    if (rank !== 0) return rank
    const aAt = a.recover_at ? Date.parse(a.recover_at) : 0
    const bAt = b.recover_at ? Date.parse(b.recover_at) : 0
    if (aAt !== bAt) return aAt - bAt
    return a.id - b.id
  })
}