import { apiClient } from '../client'
export interface AccountOpsConfig {
  enabled: boolean
  recipient: string
  balance_low: boolean
  weekly_quota: boolean
  cooldown_minutes: number
}
export interface AccountOpsEvent {
  account_id: number
  account_name: string
  kind: 'balance_low' | 'weekly_quota'
  signal: string
  http_status: number
  first_seen: string
  last_seen: string
  occurrences: number
  state: 'pending' | 'sending' | 'sent' | 'failed' | 'suppressed'
  last_sent_at: string | null
  next_send_at: string
  attempts: number
}
export interface AccountOpsSettings {
  config: AccountOpsConfig
  smtp_configured: boolean
  dropped_signals: number
  storage_failures: number
}
export async function getAccountOpsSettings(): Promise<AccountOpsSettings> {
  return (await apiClient.get('/admin/account-ops/config')).data
}
export async function saveAccountOpsSettings(config: AccountOpsConfig): Promise<AccountOpsConfig> {
  return (await apiClient.put('/admin/account-ops/config', config)).data
}
export async function getAccountOpsEvents(offset = 0): Promise<{ items: AccountOpsEvent[]; has_more: boolean }> {
  const { data } = await apiClient.get('/admin/account-ops/alerts', { params: { offset, limit: 50 } })
  return { items: data.items ?? [], has_more: data.has_more ?? false }
}
