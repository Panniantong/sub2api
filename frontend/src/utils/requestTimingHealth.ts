import { durationSeverity, firstTokenSeverity, type LatencySeverity } from './latencyHealth'

export type TimingHealth = LatencySeverity | 'neutral'
export type TimingScale = 'first' | 'total' | 'stage' | 'neutral'
// Stage bands are diagnostic reference values, not provider SLAs. Streaming
// duration, byte counts, throughput and cumulative offsets remain neutral.
export function timingHealth(value: number | null | undefined, scale: TimingScale): TimingHealth {
  if (value == null || !Number.isFinite(value) || value < 0 || scale === 'neutral') return 'neutral'
  if (scale === 'first') return firstTokenSeverity(value)
  if (scale === 'total') return durationSeverity(value)
  return value >= 5000 ? 'critical' : value >= 1000 ? 'slow' : value >= 200 ? 'warn' : 'good'
}
export const TIMING_TEXT: Record<TimingHealth, string> = {
  good: 'text-emerald-700 dark:text-emerald-400', warn: 'text-amber-700 dark:text-amber-400',
  slow: 'text-orange-700 dark:text-orange-400', critical: 'text-red-700 dark:text-red-400', neutral: 'text-slate-600 dark:text-slate-400'
}
export const TIMING_SURFACE: Record<TimingHealth, string> = {
  good: 'border-emerald-200 bg-emerald-50/70 dark:border-emerald-900 dark:bg-emerald-950/30',
  warn: 'border-amber-200 bg-amber-50/70 dark:border-amber-900 dark:bg-amber-950/30',
  slow: 'border-orange-200 bg-orange-50/70 dark:border-orange-900 dark:bg-orange-950/30',
  critical: 'border-red-200 bg-red-50/70 dark:border-red-900 dark:bg-red-950/30',
  neutral: 'border-slate-200 bg-slate-50/60 dark:border-dark-600 dark:bg-dark-800'
}
export const TIMING_BAR: Record<TimingHealth, string> = {
  good: 'bg-emerald-500', warn: 'bg-amber-500', slow: 'bg-orange-500', critical: 'bg-red-500', neutral: 'bg-slate-400 dark:bg-slate-500'
}
export function stageScale(name: string): TimingScale {
  // Parent intervals include generation and must not inherit short local-stage thresholds.
  if (name === 'handler' || name === 'forward_attempt') return 'neutral'
  return 'stage'
}
