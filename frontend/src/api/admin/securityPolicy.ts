/**
 * Admin Security Policy API endpoints
 * Custom keyword package management and session unblock for group security policy
 */

import { apiClient } from '../client'
import type { SecurityPolicyKeyword, SecurityPolicyKeywordSeed } from '@/types'

export async function listBuiltin(): Promise<{ keywords: SecurityPolicyKeywordSeed[]; count: number }> {
  const { data } = await apiClient.get<{ keywords: SecurityPolicyKeywordSeed[]; count: number }>(
    '/admin/security-policy/keywords/builtin'
  )
  return data
}

export async function listKeywords(params?: {
  group_id?: number
  include_disabled?: boolean
}): Promise<{ keywords: SecurityPolicyKeyword[] }> {
  const { data } = await apiClient.get<{ keywords: SecurityPolicyKeyword[] }>(
    '/admin/security-policy/keywords',
    {
      params: {
        ...(params?.group_id !== undefined ? { group_id: params.group_id } : {}),
        ...(params?.include_disabled ? { include_disabled: 'true' } : {})
      }
    }
  )
  return data
}

export async function createKeyword(input: {
  group_id?: number | null
  keyword: string
  category?: string
}): Promise<SecurityPolicyKeyword> {
  const { data } = await apiClient.post<SecurityPolicyKeyword>(
    '/admin/security-policy/keywords',
    input
  )
  return data
}

export async function updateKeyword(
  id: number,
  input: { keyword?: string; category?: string; enabled?: boolean }
): Promise<SecurityPolicyKeyword> {
  const { data } = await apiClient.put<SecurityPolicyKeyword>(
    `/admin/security-policy/keywords/${id}`,
    input
  )
  return data
}

export async function deleteKeyword(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(
    `/admin/security-policy/keywords/${id}`
  )
  return data
}

export async function unblockSession(input: {
  group_id: number
  api_key_id: number
}): Promise<{ unblocked: boolean }> {
  const { data } = await apiClient.post<{ unblocked: boolean }>(
    '/admin/security-policy/sessions/unblock',
    input
  )
  return data
}

export const securityPolicyAPI = {
  listBuiltin,
  listKeywords,
  createKeyword,
  updateKeyword,
  deleteKeyword,
  unblockSession
}

export default securityPolicyAPI
