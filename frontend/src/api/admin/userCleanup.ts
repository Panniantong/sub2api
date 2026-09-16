import { apiClient } from '../client'

export interface UserCleanupSummary {
  total: number
  zero_balance: number
  negative_balance: number
  server_time: string
  cutoff: string
  batch_limit: number
}

export interface UserCleanupCandidate {
  id: number
  email: string
  username: string
  balance: string
  last_used_at: string
  created_at: string
  zero_balance: boolean
}

export interface UserCleanupPreview extends UserCleanupSummary {
  preview_id: string
  expires_at: string
  candidates: UserCleanupCandidate[]
}

export interface UserCleanupResult {
  preview_id: string
  deleted_count: number
  zero_balance: number
  negative_balance: number
  skipped_count: number
  user_ids: number[]
  completed_at: string
  replayed: boolean
}

export interface UserCleanupGuard {
  user_id: number
  is_system: boolean
  is_protected: boolean
  role_protected: boolean
}

export const userCleanupAPI = {
  async summary(): Promise<UserCleanupSummary> {
    return (await apiClient.get<UserCleanupSummary>('/admin/users/cleanup/summary')).data
  },
  async preview(): Promise<UserCleanupPreview> {
    return (await apiClient.post<UserCleanupPreview>('/admin/users/cleanup/preview')).data
  },
  async execute(preview: UserCleanupPreview): Promise<UserCleanupResult> {
    return (await apiClient.post<UserCleanupResult>('/admin/users/cleanup/execute', {
      preview_id: preview.preview_id,
      expected_count: preview.candidates.length,
      confirm: true
    }, { headers: { 'Idempotency-Key': preview.preview_id } })).data
  },
  async getGuard(userId: number): Promise<UserCleanupGuard> {
    return (await apiClient.get<UserCleanupGuard>(`/admin/users/${userId}/cleanup-guard`)).data
  },
  async updateGuard(userId: number, isProtected: boolean, isSystem?: boolean): Promise<UserCleanupGuard> {
    return (await apiClient.put<UserCleanupGuard>(`/admin/users/${userId}/cleanup-guard`, {
      is_protected: isProtected,
      ...(isSystem === undefined ? {} : { is_system: isSystem })
    })).data
  }
}
