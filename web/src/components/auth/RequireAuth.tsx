import { Navigate, Outlet, useLocation } from 'react-router-dom'

import { ApiError } from '@/api/client'
import { useAuthStatus } from '@/api/hooks'

/**
 * Layout-route guard for SPEC §4's cookie-session API. There is no login UI
 * without this: before M2-125 AppShell rendered unconditionally and nothing
 * in the app ever called POST /api/login, so a session-less visitor had no
 * way to authenticate at all.
 *
 * Only a confirmed 401 sends the visitor to /login — a network/5xx error is
 * shown inline instead of bouncing someone with a perfectly valid session.
 */
export function RequireAuth() {
  const location = useLocation()
  const auth = useAuthStatus()

  if (auth.isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] text-[var(--text)]">
        <p className="text-sm text-[var(--muted)]">Checking session…</p>
      </div>
    )
  }

  if (auth.isError) {
    if (auth.error instanceof ApiError && auth.error.status === 401) {
      return <Navigate to="/login" replace state={{ from: location }} />
    }
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] px-4 text-center text-[var(--text)]">
        <p className="text-sm text-[var(--err)]" role="alert">
          Could not reach the dashboard server. Check your connection and reload.
        </p>
      </div>
    )
  }

  return <Outlet />
}
