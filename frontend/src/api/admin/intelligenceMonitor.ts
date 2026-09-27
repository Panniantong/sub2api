import { apiClient } from '@/api/client'

export interface IntelligencePrompt {
  id: string
  title: string
  prompt: string
  enabled: boolean
}

export interface IntelligenceMonitorConfig {
  enabled: boolean
  interval_seconds: number
  max_rounds: number
  model_id: string
  group_ids: number[]
  prompts: IntelligencePrompt[]
  last_run_at?: string
}

export interface IntelligenceResult {
  account_id: number
  account_name: string
  case: string
  title: string
  status: string
  cookie_host?: string
  output?: string
  html?: string
  error?: string
}

export interface IntelligenceMonitorRuntime {
  running: boolean
  round_id?: string
  started_at?: string
  finished_at?: string
  total: number
  completed: number
  account_id?: number
  account_name?: string
  prompt_id?: string
  prompt_title?: string
  message?: string
}

export interface IntelligenceRound {
  id: string
  started_at: string
  finished_at?: string
  results: IntelligenceResult[]
}

export async function getConfig(): Promise<IntelligenceMonitorConfig> {
  const { data } = await apiClient.get<IntelligenceMonitorConfig>('/admin/intelligence-monitor/config')
  return data
}

export async function updateConfig(config: IntelligenceMonitorConfig): Promise<IntelligenceMonitorConfig> {
  const { data } = await apiClient.put<IntelligenceMonitorConfig>('/admin/intelligence-monitor/config', config)
  return data
}

export async function listResults(limit = 20): Promise<IntelligenceRound[]> {
  const { data } = await apiClient.get<IntelligenceRound[]>('/admin/intelligence-monitor/results', { params: { limit } })
  return data
}

export async function runNow(): Promise<void> {
  await apiClient.post('/admin/intelligence-monitor/run')
}

export async function getRuntime(): Promise<IntelligenceMonitorRuntime> {
  const { data } = await apiClient.get<IntelligenceMonitorRuntime>('/admin/intelligence-monitor/runtime')
  return data
}

export default { getConfig, updateConfig, listResults, runNow, getRuntime }
