/**
 * Admin Global Model Pricing API endpoints
 * Site-wide per-model price overrides: matched models bill at these prices
 * across all groups and accounts.
 */

import { apiClient } from '../client'
import type { GlobalModelPrice, GlobalModelPriceInput } from '@/types'

export async function listGlobalPricing(): Promise<{ items: GlobalModelPrice[]; count: number }> {
  const { data } = await apiClient.get<{ items: GlobalModelPrice[]; count: number }>(
    '/admin/global-pricing'
  )
  return data
}

export async function createGlobalPricing(input: GlobalModelPriceInput): Promise<GlobalModelPrice> {
  const { data } = await apiClient.post<GlobalModelPrice>('/admin/global-pricing', input)
  return data
}

export async function updateGlobalPricing(
  id: number,
  input: GlobalModelPriceInput
): Promise<GlobalModelPrice> {
  const { data } = await apiClient.put<GlobalModelPrice>(`/admin/global-pricing/${id}`, input)
  return data
}

export async function deleteGlobalPricing(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/global-pricing/${id}`)
  return data
}

export async function setGlobalPricingEnabled(
  id: number,
  enabled: boolean
): Promise<GlobalModelPrice> {
  const { data } = await apiClient.post<GlobalModelPrice>(`/admin/global-pricing/${id}/enable`, {
    enabled
  })
  return data
}

export const globalPricingAPI = {
  list: listGlobalPricing,
  create: createGlobalPricing,
  update: updateGlobalPricing,
  remove: deleteGlobalPricing,
  setEnabled: setGlobalPricingEnabled
}

export default globalPricingAPI
