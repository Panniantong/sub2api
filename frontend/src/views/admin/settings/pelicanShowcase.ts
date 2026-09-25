import type { PelicanShowcaseConfig } from '@/api/admin/settings'

// Keep in sync with the backend limits in service/setting_pelican_showcase.go.
export const PELICAN_SHOWCASE_MAX_ITEMS = 100
export const PELICAN_SHOWCASE_MAX_RETENTION_DAYS = 90

export function defaultPelicanShowcaseConfig(): PelicanShowcaseConfig {
  return { group_ids: [], max_items: 20, auto_cleanup: true, retention_days: 7 }
}

function clamp(value: unknown, max: number, fallback: number): number {
  const parsed = Math.round(Number(value))
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback
  return Math.min(max, parsed)
}

/** Clamp typed limits into range so one bad number does not fail the whole settings save. */
export function sanitizePelicanShowcaseConfig(config: PelicanShowcaseConfig): PelicanShowcaseConfig {
  const defaults = defaultPelicanShowcaseConfig()
  return {
    group_ids: [...config.group_ids],
    max_items: clamp(config.max_items, PELICAN_SHOWCASE_MAX_ITEMS, defaults.max_items),
    auto_cleanup: Boolean(config.auto_cleanup),
    retention_days: clamp(config.retention_days, PELICAN_SHOWCASE_MAX_RETENTION_DAYS, defaults.retention_days),
  }
}
