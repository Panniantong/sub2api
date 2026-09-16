/**
 * Admin Support Ticket API endpoints
 */

import { apiClient } from '../client'
import type { SupportTicket, TicketReply } from '@/types'

export async function listTickets(params?: {
  status?: string
  search?: string
  limit?: number
  offset?: number
}): Promise<{ items: SupportTicket[]; count: number }> {
  const { data } = await apiClient.get<{ items: SupportTicket[]; count: number }>(
    '/admin/tickets',
    {
      params: {
        ...(params?.status ? { status: params.status } : {}),
        ...(params?.search ? { search: params.search } : {}),
        ...(params?.limit ? { limit: params.limit } : {}),
        ...(params?.offset ? { offset: params.offset } : {})
      }
    }
  )
  return data
}

export async function getTicket(id: number): Promise<SupportTicket> {
  const { data } = await apiClient.get<SupportTicket>(`/admin/tickets/${id}`)
  return data
}

export async function replyTicket(id: number, body: string): Promise<TicketReply> {
  const { data } = await apiClient.post<TicketReply>(`/admin/tickets/${id}/replies`, { body })
  return data
}

export async function closeTicket(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/admin/tickets/${id}/close`)
  return data
}

export async function getTicketStats(): Promise<{ open: number }> {
  const { data } = await apiClient.get<{ open: number }>('/admin/tickets/stats')
  return data
}

export const adminTicketsAPI = {
  list: listTickets,
  get: getTicket,
  reply: replyTicket,
  close: closeTicket,
  stats: getTicketStats
}

export default adminTicketsAPI
