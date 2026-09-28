import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Outlet } from 'react-router-dom'

import { startEventStream, type SseConnectionState } from '@/api/sse'
import type { StreamEvent } from '@/api/types'
import { BottomTabs, Sidebar } from '@/components/layout/Nav'
import { TopBar } from '@/components/layout/TopBar'

export function AppShell({ onEvent }: { onEvent?: (e: StreamEvent) => void }) {
  const queryClient = useQueryClient()
  const [live, setLive] = useState<SseConnectionState>('connecting')

  useEffect(() => {
    return startEventStream({
      queryClient,
      onState: setLive,
      onEvent,
    })
  }, [queryClient, onEvent])

  return (
    <div className="flex min-h-screen bg-[var(--bg)] text-[var(--text)]">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col pb-16 sm:pb-0">
        <TopBar live={live} />
        <main className="mx-auto w-full max-w-6xl flex-1 overflow-x-hidden px-3 py-4 sm:px-4">
          <Outlet />
        </main>
      </div>
      <BottomTabs />
    </div>
  )
}
