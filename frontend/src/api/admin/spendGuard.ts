/**
 * Admin Spend Guard API endpoints
 * Token velocity / error-rate anomaly detection with key freezing.
 */

import { apiClient } from '../client'
import type { SpendGuardEvent, SpendGuardOffender, SpendGuardSettings } from '@/types'

export async function getSpendOffenders(): Promise<{ items: SpendGuardOffender[]; count: number }> {
  const { data } = await apiClient.get<{ items: SpendGuardOffender[]; count: number }>(
    '/admin/spend-guard'
  )
  return data
}

export async function getSpendGuardSettings(): Promise<SpendGuardSettings> {
  const { data } = await apiClient.get<SpendGuardSettings>('/admin/spend-guard/settings')
  return data
}

export async function updateSpendGuardSettings(
  input: SpendGuardSettings
): Promise<SpendGuardSettings> {
  const { data } = await apiClient.put<SpendGuardSettings>('/admin/spend-guard/settings', input)
  return data
}

export async function getSpendGuardEvents(): Promise<{ items: SpendGuardEvent[]; count: number }> {
  const { data } = await apiClient.get<{ items: SpendGuardEvent[]; count: number }>(
    '/admin/spend-guard/events'
  )
  return data
}

export async function unfreezeKey(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(
    `/admin/spend-guard/keys/${id}/unfreeze`
  )
  return data
}

export const spendGuardAPI = {
  offenders: getSpendOffenders,
  getSettings: getSpendGuardSettings,
  updateSettings: updateSpendGuardSettings,
  events: getSpendGuardEvents,
  unfreeze: unfreezeKey
}

export default spendGuardAPI
