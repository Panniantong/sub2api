/**
 * Admin Margin API endpoints
 * Margin dashboard (revenue vs directory-estimated cost) and fuse management.
 */

import { apiClient } from '../client'
import type { MarginFuseEvent, MarginFuseSettings, MarginRow } from '@/types'

export async function getMarginSummary(params?: {
  hours?: number
  group_id?: number
}): Promise<{ items: MarginRow[]; count: number; hours: number }> {
  const { data } = await apiClient.get<{ items: MarginRow[]; count: number; hours: number }>(
    '/admin/margins',
    { params: { ...(params?.hours ? { hours: params.hours } : {}), ...(params?.group_id ? { group_id: params.group_id } : {}) } }
  )
  return data
}

export async function getFuseSettings(): Promise<MarginFuseSettings> {
  const { data } = await apiClient.get<MarginFuseSettings>('/admin/margins/fuse-settings')
  return data
}

export async function updateFuseSettings(input: MarginFuseSettings): Promise<MarginFuseSettings> {
  const { data } = await apiClient.put<MarginFuseSettings>('/admin/margins/fuse-settings', input)
  return data
}

export async function getFuseEvents(): Promise<{ items: MarginFuseEvent[]; count: number }> {
  const { data } = await apiClient.get<{ items: MarginFuseEvent[]; count: number }>(
    '/admin/margins/events'
  )
  return data
}

export async function unfuseChannel(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(
    `/admin/margins/channels/${id}/unfuse`
  )
  return data
}

export const marginsAPI = {
  summary: getMarginSummary,
  getSettings: getFuseSettings,
  updateSettings: updateFuseSettings,
  events: getFuseEvents,
  unfuse: unfuseChannel
}

export default marginsAPI
