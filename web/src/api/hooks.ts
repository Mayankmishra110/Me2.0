import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { apiFetch } from './client'
import type {
  AgentsResponse,
  ApprovalDetail,
  ApprovalsListResponse,
  ContentResponse,
  DecisionRequest,
  DecisionResponse,
  HealthResponse,
  JobsResponse,
  PauseRequest,
  PauseResponse,
} from './types'

export const queryKeys = {
  health: ['health'] as const,
  agents: ['agents'] as const,
  jobs: (status?: string) => ['jobs', status ?? 'all'] as const,
  approvals: (status?: string) => ['approvals', status ?? 'all'] as const,
  approval: (id: string) => ['approvals', id] as const,
  content: (params?: { channel?: string; stage?: string }) =>
    ['content', params?.channel ?? 'all', params?.stage ?? 'all'] as const,
}

export function useHealth() {
  return useQuery({
    queryKey: queryKeys.health,
    queryFn: () => apiFetch<HealthResponse>('/api/health'),
  })
}

export function useAgents() {
  return useQuery({
    queryKey: queryKeys.agents,
    queryFn: () => apiFetch<AgentsResponse>('/api/agents'),
  })
}

export function useJobs(status?: string) {
  const qs = status ? `?status=${encodeURIComponent(status)}` : ''
  return useQuery({
    queryKey: queryKeys.jobs(status),
    queryFn: () => apiFetch<JobsResponse>(`/api/jobs${qs}`),
  })
}

export function useApprovals(status = 'pending') {
  return useQuery({
    queryKey: queryKeys.approvals(status),
    queryFn: () =>
      apiFetch<ApprovalsListResponse>(`/api/approvals?status=${encodeURIComponent(status)}`),
  })
}

export function useApproval(id: string | undefined) {
  return useQuery({
    queryKey: queryKeys.approval(id ?? ''),
    queryFn: () => apiFetch<ApprovalDetail>(`/api/approvals/${id}`),
    enabled: Boolean(id),
  })
}

export function useContent(params?: { channel?: string; stage?: string }) {
  const search = new URLSearchParams()
  if (params?.channel) search.set('channel', params.channel)
  if (params?.stage) search.set('stage', params.stage)
  const qs = search.toString() ? `?${search}` : ''
  return useQuery({
    queryKey: queryKeys.content(params),
    queryFn: () => apiFetch<ContentResponse>(`/api/content${qs}`),
  })
}

export function usePauseAll() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: PauseRequest) =>
      apiFetch<PauseResponse>('/api/pause', {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: queryKeys.health })
      await qc.invalidateQueries({ queryKey: queryKeys.agents })
    },
  })
}

export function useApprovalDecision(id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: DecisionRequest) =>
      apiFetch<DecisionResponse>(`/api/approvals/${id}/decision`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: queryKeys.approvals() })
      await qc.invalidateQueries({ queryKey: queryKeys.approval(id) })
      await qc.invalidateQueries({ queryKey: queryKeys.health })
    },
  })
}
