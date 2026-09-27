import { apiClient } from '@/api/client'

export interface IntelligenceTestEvent {
  type: string
  text?: string
  error?: string
  cookie_host?: string
  timestamp?: string
  success?: boolean
}

export interface IntelligenceTestResult {
  id: string
  account_id: number
  account_name: string
  case: string
  title?: string
  prompt: string
  model_id: string
  status: 'running' | 'success' | 'error'
  cookie_host?: string
  output?: string
  html?: string
  error?: string
  started_at: string
  finished_at?: string
  events?: IntelligenceTestEvent[]
}

export async function start(accountID: number, payload: { case: string; prompt: string; model_id: string }) {
  return (await apiClient.post<IntelligenceTestResult>(`/admin/accounts/${accountID}/intelligence-test`, payload)).data
}

export async function list(accountID: number) {
  return (await apiClient.get<IntelligenceTestResult[]>(`/admin/accounts/${accountID}/intelligence-test/results`)).data
}

export default { start, list }
