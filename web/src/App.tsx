import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useNavigate } from 'react-router-dom'

import { onUnauthorized } from '@/api/authEvents'
import type { StreamEvent } from '@/api/types'
import { RequireAuth } from '@/components/auth/RequireAuth'
import { AppShell } from '@/components/layout/AppShell'
import { ApprovalDetailPage, ApprovalsPage } from '@/pages/ApprovalsPage'
import { BuilderPage } from '@/pages/BuilderPage'
import { HomePage } from '@/pages/HomePage'
import { LoginPage } from '@/pages/LoginPage'
import { LogsPage } from '@/pages/LogsPage'
import { MorePage } from '@/pages/MorePage'
import { RevenuePage } from '@/pages/RevenuePage'

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

/** Mounted inside the router so a 401 on any API call (session expired
 * mid-visit) sends the user to /login instead of leaving a broken page up. */
function UnauthorizedRedirect() {
  const navigate = useNavigate()
  useEffect(() => onUnauthorized(() => navigate('/login', { replace: true })), [navigate])
  return null
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
        <UnauthorizedRedirect />
        <Routes>
          <Route path="login" element={<LoginPage />} />
          <Route element={<RequireAuth />}>
            <Route element={<AppShell onEvent={onEvent} />}>
              <Route index element={<HomePage />} />
              <Route path="approvals" element={<ApprovalsPage />} />
              <Route path="approvals/:id" element={<ApprovalDetailPage />} />
              <Route path="builder" element={<BuilderPage />} />
              <Route path="revenue" element={<RevenuePage />} />
              <Route path="logs" element={<LogsPage events={events} />} />
              <Route path="more" element={<MorePage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}

export default App
