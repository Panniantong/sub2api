import { apiClient } from '../client'
import type { ScheduledTestPlan, ScheduledTestResult } from '@/types'
export async function listQualityPlans(): Promise<ScheduledTestPlan[]> {
  const { data } = await apiClient.get<ScheduledTestPlan[]>('/admin/account-quality-plans')
  return data ?? []
}
export async function runQualityPlan(id: number): Promise<void> {
  await apiClient.post(`/admin/account-quality-plans/${id}/run`)
}

export interface QualityOperation extends ScheduledTestResult {
  account_id: number
  account_name: string
  passed_count: number
  total_count: number
  result_ids: number[]
}
export async function listQualityOperations(beforeId = 0): Promise<{ items: QualityOperation[]; next_cursor: number }> {
  const { data } = await apiClient.get('/admin/account-quality-results', { params: { before_id: beforeId } })
  return { items: data.items ?? [], next_cursor: data.next_cursor ?? 0 }
}
