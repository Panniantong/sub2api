import { apiClient } from '../client'

export interface AccountTrafficPolicy {
  strict_rpm_enabled: boolean; rpm: number; burst: number
  adaptive_enabled: boolean; adaptive_mode: 'observe' | 'automatic'
  min_concurrency: number; failure_threshold: number; failure_window_seconds: number; recovery_seconds: number
}
export interface AccountTrafficState {
  effective_concurrency: number; recommended_concurrency: number; in_flight: number; requests_last_minute: number
  accepted: number; rejected_rpm: number; rejected_concurrency: number; upstream_429: number; upstream_5xx: number
  completed: number; average_duration_ms: number; last_adjustment_at?: string
}
export interface AccountTrafficResponse { policy: AccountTrafficPolicy; state?: AccountTrafficState | null; state_available: boolean; hard_limit: number }
export const defaultTrafficPolicy = (): AccountTrafficPolicy => ({ strict_rpm_enabled: false, rpm: 60, burst: 5, adaptive_enabled: false, adaptive_mode: 'observe', min_concurrency: 1, failure_threshold: 3, failure_window_seconds: 60, recovery_seconds: 60 })
const integerIn = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
export function normalizeTrafficDraft(value: AccountTrafficPolicy): AccountTrafficPolicy {
  const policy = { ...value }, defaults = defaultTrafficPolicy()
  if (!policy.strict_rpm_enabled) {
    if (!integerIn(policy.rpm, 1, 60000)) policy.rpm = defaults.rpm
    if (!integerIn(policy.burst, 1, policy.rpm)) policy.burst = Math.min(defaults.burst, policy.rpm)
  }
  if (!policy.adaptive_enabled) {
    if (!integerIn(policy.min_concurrency, 1, 10000)) policy.min_concurrency = defaults.min_concurrency
    if (!integerIn(policy.failure_threshold, 1, 100)) policy.failure_threshold = defaults.failure_threshold
    if (!integerIn(policy.failure_window_seconds, 10, 3600)) policy.failure_window_seconds = defaults.failure_window_seconds
    if (!integerIn(policy.recovery_seconds, 10, 3600)) policy.recovery_seconds = defaults.recovery_seconds
  }
  return policy
}
export function trafficPolicyError(policy: AccountTrafficPolicy, hardLimit: number): string {
  if (policy.strict_rpm_enabled && (!integerIn(policy.rpm, 1, 60000) || !integerIn(policy.burst, 1, policy.rpm))) return 'RPM 必须为 1–60000 的整数，突发额度必须为正整数，突发额度不能大于 RPM'
  if (policy.adaptive_enabled) {
    if (!integerIn(hardLimit, 1, Number.MAX_SAFE_INTEGER) || !integerIn(policy.min_concurrency, 1, Math.min(hardLimit, 10000))) return '最低并发必须为正整数，且不能超过账号并发上限'
    if (!integerIn(policy.failure_threshold, 1, 100)) return '触发失败次数必须为 1–100 的整数'
    if (!integerIn(policy.failure_window_seconds, 10, 3600) || !integerIn(policy.recovery_seconds, 10, 3600)) return '失败统计窗口和恢复周期必须为 10–3600 秒的整数'
    if (!['observe', 'automatic'].includes(policy.adaptive_mode)) return '请选择有效的运行方式'
  }
  return ''
}
export const accountTrafficAPI = {
  async get(id: number, signal?: AbortSignal) { return (await apiClient.get<AccountTrafficResponse>(`/admin/accounts/${id}/traffic-control`, { signal })).data },
  async save(id: number, policy: AccountTrafficPolicy) { return (await apiClient.put<AccountTrafficResponse>(`/admin/accounts/${id}/traffic-control`, policy)).data }
}
