/**
 * Admin Tiered Routing API endpoints
 * VIP users prefer premium-pool accounts; others use all accounts.
 */

import { apiClient } from '../client'
import type { TieredRoutingSettings } from '@/types'

export async function getTieredRoutingSettings(): Promise<TieredRoutingSettings> {
  const { data } = await apiClient.get<TieredRoutingSettings>('/admin/tiered-routing/settings')
  return data
}

export async function updateTieredRoutingSettings(
  input: TieredRoutingSettings
): Promise<TieredRoutingSettings> {
  const { data } = await apiClient.put<TieredRoutingSettings>(
    '/admin/tiered-routing/settings',
    input
  )
  return data
}

export const tieredRoutingAPI = {
  getSettings: getTieredRoutingSettings,
  updateSettings: updateTieredRoutingSettings
}

export default tieredRoutingAPI
