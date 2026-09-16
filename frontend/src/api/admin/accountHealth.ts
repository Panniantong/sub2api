/**
 * Admin Account Health API endpoints
 * Site-wide account health scores: snapshot list, thresholds, isolate/resume.
 */

import { apiClient } from '../client'
import type { AccountHealthSnapshot, AccountHealthSettings } from '@/types'

export async function getHealthSnapshot(): Promise<{ items: AccountHealthSnapshot[]; count: number }> {
  const { data } = await apiClient.get<{ items: AccountHealthSnapshot[]; count: number }>(
    '/admin/account-health'
  )
  return data
}

export async function getHealthSettings(): Promise<AccountHealthSettings> {
  const { data } = await apiClient.get<AccountHealthSettings>('/admin/account-health/settings')
  return data
}

export async function updateHealthSettings(input: AccountHealthSettings): Promise<AccountHealthSettings> {
  const { data } = await apiClient.put<AccountHealthSettings>(
    '/admin/account-health/settings',
    input
  )
  return data
}

export async function isolateAccount(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(
    `/admin/account-health/${id}/isolate`
  )
  return data
}

export async function resumeAccount(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(
    `/admin/account-health/${id}/resume`
  )
  return data
}

export const accountHealthAPI = {
  snapshot: getHealthSnapshot,
  getSettings: getHealthSettings,
  updateSettings: updateHealthSettings,
  isolate: isolateAccount,
  resume: resumeAccount
}

export default accountHealthAPI
