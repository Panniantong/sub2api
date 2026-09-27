import { apiClient } from '../client'
import type { OpenAICodexCookieLibraryEntry } from './settings'
import type { PaginatedResponse } from '@/types'

export interface CookieSettings {
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
  cookie_refresh_before_seconds: number
  cookie_rotation_enabled: boolean
  cookie_host_binding_seconds: number
  cookie_host_rotation_before_seconds: number
}

export interface CookieLog {
  id: string
  kind?: 'validation' | 'scheduler' | string
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
export async function saveSettings(value: CookieSettings) {
  return (await apiClient.put<CookieSettings>('/admin/settings/cookie', value)).data
}
export async function getLibrary() {
  return (await apiClient.get<OpenAICodexCookieLibraryEntry[]>('/admin/settings/openai-codex-cookie-library')).data
}
export async function getLogs(limit?: number) {
  return (await apiClient.get<CookieLog[]>('/admin/settings/cookie/logs', { params: limit ? { limit } : undefined })).data
}
export async function getValidationLogs(params: { account_id?: number; page?: number; page_size?: number } = {}) {
  return (await apiClient.get<PaginatedResponse<CookieLog>>('/admin/settings/cookie/validation-logs', {
    params: {
      account_id: params.account_id,
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
