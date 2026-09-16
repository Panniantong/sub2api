import { apiClient } from './client'

export type TestStatus = 'waiting' | 'queued' | 'running' | 'completed' | 'cancelled' | 'success' | 'failed' | 'rate_limited' | 'account_error' | 'model_error' | 'request_error' | 'network_error' | 'suspected_degradation'
export interface TestAssessment {
  evaluator_version?: number
  answer_verdict?: 'correct' | 'incorrect' | 'undetermined' | 'not_evaluated'
  format_verdict?: 'compliant' | 'non_compliant' | 'not_required' | 'not_evaluated'
  capability_verdict?: 'insufficient_evidence'
  image_state?: 'ready' | 'sanitized' | 'unavailable'
  reason?: string; format_reason?: string; limitation?: string
  expected_answer?: string; actual_answer?: string; original_judgment?: unknown
  [key: string]: unknown
}
export interface TestRecord {
  id: number; account_id: number; test_type: string; status: TestStatus
  score: number | null; result: string; result_image: string; error_message?: string
  duration_ms: number; model: string; anti_degradation: boolean
  started_at: string | null; finished_at: string | null; created_at: string
  input?: string; raw_response?: unknown; config_snapshot?: unknown
  queue_reason?: string
  available_at?: string
  raw_truncated?: boolean; evaluation?: TestAssessment
}
export interface TestSummary {
  test_type: string; latest: TestRecord | null; history_count: number
  latest_completed?: TestRecord | null
  consecutive_anomalies: number; risk: string
}
export interface TestAccount {
  account_id: number; name: string; notes?: string; platform: string; account_type: string
  account_status: string; group_ids: number[]; anti_degradation: boolean; tests: TestSummary[]
}
export interface TestOverview {
  total_accounts: number; tested_today: number; success_accounts: number
  abnormal_accounts: number; suspected_degradation: number
  review_accounts?: number
}
export interface TestPage<T> { items: T[]; total: number; page: number; page_size: number }
export interface TestConfig {
  answer_type?: 'auto' | 'number' | 'text'
  answer_unit?: string
  answer_unit_mode?: 'legacy' | 'none' | 'configured'
  answer_format?: 'answer_line' | 'free_text'
  prompt: string; model: string; evaluator: 'svg_structure' | 'exact_answer'
  expected_answer?: string; timeout_seconds: number
}
export interface TestSetting {
  test_type: string; name?: string; enabled: boolean; user_visible: boolean
  config: TestConfig; updated_at?: string
}
export type PublicTestRecord = Pick<TestRecord, 'id' | 'account_id' | 'test_type' | 'status' | 'score' | 'result' | 'result_image' | 'duration_ms' | 'model' | 'created_at' | 'finished_at' | 'evaluation'>
export interface TestSubmission { records: TestRecord[]; reused: boolean; created_count?: number; reused_count?: number }
export interface PublicTestAccount { account_id: number; platform: string; account_type: string; tests: PublicTestRecord[] }
export type TestFilters = Record<string, string | number | boolean | undefined>
const base = '/admin/intelligent-tests'

export function newTestRequestKey(): string {
  return globalThis.crypto?.randomUUID?.() ?? `test-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
export const intelligentTestsAPI = {
  async accounts(params: TestFilters, signal?: AbortSignal) {
    return (await apiClient.get<TestPage<TestAccount> & { overview: TestOverview }>(`${base}/accounts`, { params, signal })).data
  },
  async records(params: TestFilters, signal?: AbortSignal) {
    return (await apiClient.get<TestPage<TestRecord>>(`${base}/records`, { params, signal })).data
  },
  async detail(id: number) { return (await apiClient.get<TestRecord>(`${base}/records/${id}`)).data },
  async run(account_ids: number[], test_types: string[], idempotency_key: string, models?: Record<string, string>) {
    return (await apiClient.post<TestSubmission>(`${base}/run`, { account_ids, test_types, idempotency_key, ...(models ? { models } : {}) })).data
  },
  async image(id: number, publicView = false, signal?: AbortSignal) { return (await apiClient.get<Blob>(publicView ? `/account-capabilities/results/${id}/image` : `${base}/records/${id}/image`, { responseType: 'blob', signal })).data },
  async cancel(id: number) { return (await apiClient.post<TestRecord>(`${base}/records/${id}/cancel`)).data },
  async reevaluate(id: number) { return (await apiClient.post<TestRecord>(`${base}/records/${id}/reevaluate`)).data },
  async previewEvaluation(output: string, config: TestConfig) { return (await apiClient.post<TestRecord>(`${base}/evaluate-preview`, { output, config })).data },
  async settings() { return (await apiClient.get<{ items: TestSetting[] }>(`${base}/settings`)).data.items },
  async saveSetting(setting: TestSetting) {
    const snapshot: TestSetting = { ...setting, config: { ...setting.config } }
    await apiClient.put(`${base}/settings/${encodeURIComponent(snapshot.test_type)}`, {
      enabled: snapshot.enabled, user_visible: snapshot.user_visible, config: snapshot.config
    })
    return snapshot
  },
  async publicAccounts(page = 1) { return (await apiClient.get<TestPage<PublicTestAccount>>('/account-capabilities', { params: { page, page_size: 12 } })).data },
  async publicTests(accountId: number, page = 1) { return (await apiClient.get<TestPage<PublicTestRecord>>(`/account-capabilities/${accountId}/tests`, { params: { page, page_size: 12 } })).data },
  async publicDetail(id: number) { return (await apiClient.get<PublicTestRecord>(`/account-capabilities/results/${id}`)).data }
}
