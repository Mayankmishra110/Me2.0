import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCallback, useMemo, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'

import type { StreamEvent } from '@/api/types'
import { AppShell } from '@/components/layout/AppShell'
import { ApprovalDetailPage, ApprovalsPage } from '@/pages/ApprovalsPage'
import { BuilderPage } from '@/pages/BuilderPage'
import { HomePage } from '@/pages/HomePage'
import { LogsPage } from '@/pages/LogsPage'
import { MorePage } from '@/pages/MorePage'

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 5_000,
        refetchOnWindowFocus: false,
        retry: 1,
      },
    },
  })
}

export function App() {
  const queryClient = useMemo(() => createQueryClient(), [])
  const [events, setEvents] = useState<StreamEvent[]>([])
  const onEvent = useCallback((event: StreamEvent) => {
    setEvents((prev) => [event, ...prev].slice(0, 100))
  }, [])

  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<AppShell onEvent={onEvent} />}>
            <Route index element={<HomePage />} />
            <Route path="approvals" element={<ApprovalsPage />} />
            <Route path="approvals/:id" element={<ApprovalDetailPage />} />
            <Route path="builder" element={<BuilderPage />} />
            <Route path="logs" element={<LogsPage events={events} />} />
            <Route path="more" element={<MorePage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}

export default App
