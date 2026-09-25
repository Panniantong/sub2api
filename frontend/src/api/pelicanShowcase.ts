/**
 * Pelican showcase API: the user gallery of scheduled Pelican HTML results.
 * Snapshots never carry account identity — only the group, model and timing.
 */

import { apiClient } from './client'

export interface PelicanShowcaseItem {
  id: number
  group_id: number
  model_id: string
  reasoning_effort: string
  latency_ms: number
  generated_at: string
  /** Raw model output; only returned by getItem. Render through extractPelicanHtml. */
  response_text?: string
}

export interface PelicanShowcaseGroup {
  id: number
  name: string
  platform: string
  items: PelicanShowcaseItem[]
}

export interface PelicanShowcaseView {
  enabled: boolean
  /** Newest snapshots kept per group. */
  max_items: number
  /** Snapshots older than this are cleaned up; 0 = auto cleanup off. */
  retention_days: number
  groups: PelicanShowcaseGroup[]
}

export async function getShowcase(options?: { signal?: AbortSignal }): Promise<PelicanShowcaseView> {
  const { data } = await apiClient.get<PelicanShowcaseView>('/pelican-showcase', { signal: options?.signal })
  return data
}

export async function getShowcaseItem(id: number): Promise<PelicanShowcaseItem> {
  const { data } = await apiClient.get<PelicanShowcaseItem>(`/pelican-showcase/items/${id}`)
  return data
}

/** Admin only: take one snapshot off the gallery. */
export async function removeShowcaseItem(id: number): Promise<void> {
  await apiClient.delete(`/admin/pelican-showcase/items/${id}`)
}

export const pelicanShowcaseAPI = { getShowcase, getShowcaseItem, removeShowcaseItem }

export default pelicanShowcaseAPI
