/**
 * Admin Dynamic Proxy (uDeal ticket harvesting) API endpoints
 */

import { apiClient } from '../client'

export interface DynamicProxyBindingView {
  account_id: number
  account_name: string
  ip: string
  port: string
  origin_ip: string
  bound_at: string
  expires_at: string
  last_keepalive: string
}

export interface DynamicProxyStatus {
  enabled: boolean
  iplist_url: string
  user: string
  pass_set: boolean
  bindings: DynamicProxyBindingView[] | null
}

export interface DynamicProxyConfigRequest {
  enabled: boolean
  iplist_url: string
  user: string
  pass: string
}

export async function getStatus(): Promise<DynamicProxyStatus> {
  const { data } = await apiClient.get<DynamicProxyStatus>('/admin/dynamic-proxy/status')
  return data
}

export async function updateConfig(req: DynamicProxyConfigRequest): Promise<DynamicProxyStatus> {
  const { data } = await apiClient.put<DynamicProxyStatus>('/admin/dynamic-proxy/config', req)
  return data
}

export async function rebind(accountId: number): Promise<DynamicProxyBindingView> {
  const { data } = await apiClient.post<DynamicProxyBindingView>(
    `/admin/dynamic-proxy/accounts/${accountId}/rebind`
  )
  return data
}

export async function unbind(accountId: number): Promise<{ unbound: number }> {
  const { data } = await apiClient.delete<{ unbound: number }>(
    `/admin/dynamic-proxy/accounts/${accountId}/binding`
  )
  return data
}

export default { getStatus, updateConfig, rebind, unbind }
