/**
 * User Billing API endpoints: monthly statement and usage CSV export.
 */

import { apiClient } from './client'
import type { BillingStatement } from '@/types'

export async function getStatement(year: number, month: number): Promise<BillingStatement> {
  const { data } = await apiClient.get<BillingStatement>('/billing/statement', {
    params: { year, month }
  })
  return data
}

export async function exportUsageCSV(start: string, end: string): Promise<Blob> {
  try {
    const resp = await apiClient.get('/billing/export', {
      params: { start, end },
      responseType: 'blob'
    })
    return resp.data as Blob
  } catch (err: any) {
    // 失败时服务端返回的是 JSON envelope 的 Blob，需手动解析出 message。
    const raw = err?.response?.data
    if (raw instanceof Blob) {
      try {
        const text = await raw.text()
        const parsed = JSON.parse(text) as { message?: string }
        if (parsed.message) {
          throw { ...err, message: parsed.message }
        }
      } catch (parseErr: any) {
        if (parseErr?.message && parseErr.message !== 'Unexpected token') {
          throw parseErr
        }
      }
    }
    throw err
  }
}

export const billingAPI = {
  statement: getStatement,
  exportCSV: exportUsageCSV
}

export default billingAPI
