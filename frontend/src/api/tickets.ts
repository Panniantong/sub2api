/**
 * User Support Ticket API endpoints
 */

import { apiClient } from './client'
import type { SupportTicket, TicketReply } from '@/types'

export async function listMyTickets(): Promise<{ items: SupportTicket[]; count: number }> {
  const { data } = await apiClient.get<{ items: SupportTicket[]; count: number }>('/tickets')
  return data
}

export async function createTicket(input: { subject: string; body: string }): Promise<SupportTicket> {
  const { data } = await apiClient.post<SupportTicket>('/tickets', input)
  return data
}

export async function getMyTicket(id: number): Promise<SupportTicket> {
  const { data } = await apiClient.get<SupportTicket>(`/tickets/${id}`)
  return data
}

export async function replyMyTicket(id: number, body: string): Promise<TicketReply> {
  const { data } = await apiClient.post<TicketReply>(`/tickets/${id}/replies`, { body })
  return data
}

export async function closeMyTicket(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/tickets/${id}/close`)
  return data
}

export const ticketsAPI = {
  list: listMyTickets,
  create: createTicket,
  get: getMyTicket,
  reply: replyMyTicket,
  close: closeMyTicket
}

export default ticketsAPI
