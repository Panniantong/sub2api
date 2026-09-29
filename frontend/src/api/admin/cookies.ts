import { apiClient } from '../client'
import type { OpenAICodexCookieLibraryEntry } from './settings'
import type { PaginatedResponse } from '@/types'

export interface CookieSettings {
  local_harvest_disabled?: boolean
  remote_sync_enabled?: boolean
  remote_sync_url?: string
  remote_sync_admin_key?: string
  remote_sync_interval_seconds?: number
  degraded_group_id?: number
  degraded_group_name?: string
  harvest_policy?: CookieHarvestPolicy
  enabled: boolean
  model: string
  interval_seconds: number
  account_id?: number
  account_ids: number[]
  group_ids: number[]
  rotation_account_ids: number[]
  rotation_group_ids: number[]
  cookie_host_scheduling_guard_enabled: boolean
  proxy_urls: string[]
  managed_proxy_ids: number[]
  use_all_managed_proxies: boolean
  cookie_proxy_schedule_mode: 'round_robin' | 'dynamic'
  cookie_harvest_concurrency: number
  cookie_proxy_learning_attempts: number
  dynamic_proxy_fill_host_cookie: boolean
  host_whitelist: string[]
  auto_validate_host: boolean
  ws_enabled: boolean
  ws_connections: number
  ws_ttl_seconds: number
  ws_host_cooldown_seconds: number
  cookie_host_validation_failure_cooldown_seconds: number
  cookie_refresh_before_seconds: number
  cookie_rotation_enabled: boolean
  cookie_host_binding_seconds: number
  cookie_host_rotation_before_seconds: number
}

export interface CookieHarvestPolicy {
  explore_percent: number
  fill_percent: number
  refresh_percent: number
  learning_samples: number
  failure_threshold: number
  failure_backoff_seconds: number
  target_miss_limit: number
  target_backoff_seconds: number
  exploration_revisit_seconds: number
}

export const defaultHarvestPolicy = (): CookieHarvestPolicy => ({ explore_percent: 30, fill_percent: 50, refresh_percent: 20, learning_samples: 20, failure_threshold: 3, failure_backoff_seconds: 60, target_miss_limit: 3, target_backoff_seconds: 120, exploration_revisit_seconds: 900 })

export interface CookieLog {
  task?: string
  target_host?: string
  id: string
  kind?: 'validation' | 'scheduler' | 'remote_sync' | string
  attempt_id?: string
  stage?: string
  account_id: number
  account_name: string
  created_at: string
  model: string
  proxy: string
  proxy_username?: string
  status_code: number
  success: boolean
  message: string
  host: string
  cookie: string
  payload?: Record<string, unknown>
  session_id: string
  response: string
  validation_enabled?: boolean
  validation_result?: string
  validation_response?: string
  binding_status?: string
  binding_host?: string
}

export interface CookieProxyHostMemoryEntry {
  host: string
  count: number
}

export interface CookieProxyHostMemory {
  successful_samples?: number
  sample_target?: number
  backoff_until?: string
  stats?: { attempts: number; successes: number; failures: number; unauthorized: number; new_hosts: number; target_hits: number; tasks?: Record<string, number> }
  proxy: string
  proxy_username?: string
  requests: number
  limit: number
  completed: boolean
  hosts: CookieProxyHostMemoryEntry[]
  updated_at?: string
}

export async function getSettings() {
  return (await apiClient.get<CookieSettings>('/admin/settings/cookie')).data
}

export async function getHarvestRuntime() {
  return (await apiClient.get<Record<string, number>>('/admin/settings/cookie/runtime')).data
}

export interface CookieDashboard {
  healthy_bound_accounts: number
  harvest: Record<string, number>
  rotation_accounts: number
  rotation_running: number
  degraded_accounts: number
  harvest_enabled: boolean
  rotation_enabled: boolean
  harvest_concurrency: number
  updated_at: string
}

export async function getDashboard() {
  return (await apiClient.get<CookieDashboard>('/admin/settings/cookie/dashboard')).data
}
export async function saveSettings(value: CookieSettings) {
  return (await apiClient.put<CookieSettings>('/admin/settings/cookie', value)).data
}
export async function getLibrary() {
  return (await apiClient.get<OpenAICodexCookieLibraryEntry[]>('/admin/settings/openai-codex-cookie-library')).data
}
export async function getLogs(limit?: number) {
  return (await apiClient.get<CookieLog[]>('/admin/settings/cookie/logs', { params: limit ? { limit } : undefined })).data
}
export async function getValidationHosts(account_id: number) {
  return (await apiClient.get<string[]>('/admin/settings/cookie/validation-logs', { params: { account_id, hosts_only: true } })).data
}
export async function getValidationLogs(params: { account_id?: number; page?: number; page_size?: number; host?: string } = {}) {
  return (await apiClient.get<PaginatedResponse<CookieLog>>('/admin/settings/cookie/validation-logs', {
    params: {
      account_id: params.account_id,
      host: params.host,
      page: params.page ?? 1,
      page_size: params.page_size ?? 20
    }
  })).data
}

export async function getProxyHostMemories() {
  return (await apiClient.get<CookieProxyHostMemory[]>('/admin/settings/cookie/proxy-host-memories')).data
}

export async function resetProxyHostMemory(proxy: string, proxyUsername = '') {
  return (await apiClient.post<{ reset: boolean }>('/admin/settings/cookie/proxy-host-memories/reset', { proxy, proxy_username: proxyUsername })).data
}

export async function resetAllProxyHostMemories() {
  return (await apiClient.post<{ reset: boolean }>('/admin/settings/cookie/proxy-host-memories/reset', { all: true })).data
}
