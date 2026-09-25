import { apiClient } from '../client'
export interface TimingSpan { name: string; start_ms: number; end_ms: number; attempt?: number }
export interface TimingAttempt {
  kind: string; parent?: number; number: number; account_id: number; proxy_id: number; start_ms: number; end_ms: number | null
  status: number; error?: string; terminal?: string; cleanup_canceled?: boolean; reused: boolean | null; request_bytes: number; response_bytes: number
  body_eof: boolean; events: Record<string, number>
}
export interface RequestTiming {
  outcome?: string; client_disconnect: boolean; version: number; trace_id: string; started_at: string; total_ms: number; status: number; canceled: boolean
  truncated: boolean; body_bytes: number; body_expected: number; body_complete: boolean; body_read_ms: number
  downstream_bytes: number; downstream_write_ms: number; downstream_error: boolean; ttft_mode?: string; terminal?: string
  events: Record<string, number>; spans: TimingSpan[]; attempts: TimingAttempt[]
}
export async function getUsageTiming(id: number, signal?: AbortSignal): Promise<{ traces: RequestTiming[]; retention_days: number }> {
  const { data } = await apiClient.get(`/admin/usage/${id}/timing`, { signal })
  return data
}
