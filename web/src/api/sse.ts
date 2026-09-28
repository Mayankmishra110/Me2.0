import type { QueryClient } from '@tanstack/react-query'

import { queryKeys } from './hooks'
import type { SseEventKind, StreamEvent } from './types'

const INVALIDATIONS: Record<SseEventKind, readonly (readonly string[])[]> = {
  'job.updated': [queryKeys.jobs(), queryKeys.jobs('dead'), queryKeys.content()],
  'approval.created': [queryKeys.approvals('pending'), queryKeys.approvals()],
  'approval.decided': [queryKeys.approvals('pending'), queryKeys.approvals(), queryKeys.health],
  'agent.state': [queryKeys.agents, queryKeys.health],
  alert: [queryKeys.health, queryKeys.jobs('dead')],
}

export type SseConnectionState = 'connecting' | 'live' | 'offline'

export interface StartSseOptions {
  queryClient: QueryClient
  onState?: (state: SseConnectionState) => void
  onEvent?: (event: StreamEvent) => void
  url?: string
}

export function startEventStream(options: StartSseOptions): () => void {
  const url = options.url ?? '/api/events/stream'
  let closed = false
  let source: EventSource | null = null
  let attempt = 0
  let timer: ReturnType<typeof setTimeout> | null = null

  const clearTimer = () => {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  const connect = () => {
    if (closed) return
    options.onState?.(attempt === 0 ? 'connecting' : 'connecting')
    source = new EventSource(url)

    source.onopen = () => {
      attempt = 0
      options.onState?.('live')
    }

    source.onmessage = (msg) => {
      try {
        const event = JSON.parse(msg.data) as StreamEvent
        options.onEvent?.(event)
        const keys = INVALIDATIONS[event.kind] ?? []
        for (const key of keys) {
          void options.queryClient.invalidateQueries({ queryKey: key })
        }
        if (event.kind === 'approval.decided' && typeof event.payload.id === 'string') {
          void options.queryClient.invalidateQueries({
            queryKey: queryKeys.approval(event.payload.id),
          })
        }
      } catch {
        // ignore malformed frames
      }
    }

    source.onerror = () => {
      source?.close()
      source = null
      if (closed) return
      options.onState?.('offline')
      const delay = Math.min(30_000, 1000 * 2 ** attempt)
      attempt += 1
      clearTimer()
      timer = setTimeout(connect, delay)
    }
  }

  connect()

  return () => {
    closed = true
    clearTimer()
    source?.close()
    source = null
  }
}
