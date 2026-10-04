import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { RequireAuth } from '@/components/auth/RequireAuth'
import { server } from '@/mocks/server'

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/login" element={<div>Login screen</div>} />
          <Route element={<RequireAuth />}>
            <Route path="/" element={<div>Dashboard home</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('RequireAuth', () => {
  it('redirects an unauthenticated visit to / to /login', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ error: 'unauthorized' }, { status: 401 })),
    )

    renderAt('/')

    expect(await screen.findByText('Login screen')).toBeInTheDocument()
    expect(screen.queryByText('Dashboard home')).not.toBeInTheDocument()
  })

  it('renders the dashboard for an authenticated visit (no redirect)', async () => {
    // Default mock handler for /api/agents returns 200 — i.e. "has a valid session".
    renderAt('/')

    expect(await screen.findByText('Dashboard home')).toBeInTheDocument()
    expect(screen.queryByText('Login screen')).not.toBeInTheDocument()
  })
})
