import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'

import { LoginPage } from '@/pages/LoginPage'
import { server } from '@/mocks/server'

function renderLogin(
  initialEntries: Parameters<typeof MemoryRouter>[0]['initialEntries'] = ['/login'],
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<div>Dashboard home</div>} />
          <Route path="/approvals" element={<div>Approvals screen</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('LoginPage', () => {
  beforeEach(() => {
    // LoginPage checks /api/agents itself (to skip the form if already
    // logged in) — treat the viewer as session-less so the form renders.
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ error: 'unauthorized' }, { status: 401 })),
    )
  })

  it('submits the token to POST /api/login and redirects to / on success', async () => {
    const user = userEvent.setup()
    let body: unknown
    server.use(
      http.post('/api/login', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(
          { ok: true },
          { headers: { 'Set-Cookie': 'session=mock; Path=/' } },
        )
      }),
    )

    renderLogin()
    await user.type(screen.getByLabelText(/token/i), 'secret-token')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => expect(body).toEqual({ token: 'secret-token' }))
    expect(await screen.findByText('Dashboard home')).toBeInTheDocument()
  })

  it('redirects back to the page the visitor originally requested', async () => {
    const user = userEvent.setup()
    server.use(http.post('/api/login', () => HttpResponse.json({ ok: true })))

    renderLogin([{ pathname: '/login', state: { from: { pathname: '/approvals' } } }])

    await user.type(screen.getByLabelText(/token/i), 'secret-token')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    expect(await screen.findByText('Approvals screen')).toBeInTheDocument()
  })

  it('shows "Incorrect token" on a 401 and does not navigate', async () => {
    const user = userEvent.setup()
    server.use(
      http.post('/api/login', () => HttpResponse.json({ error: 'unauthorized' }, { status: 401 })),
    )

    renderLogin()
    await user.type(screen.getByLabelText(/token/i), 'wrong-token')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent('Incorrect token')
    expect(screen.queryByText('Dashboard home')).not.toBeInTheDocument()
  })

  it('shows a generic retry message on a network error', async () => {
    const user = userEvent.setup()
    server.use(http.post('/api/login', () => HttpResponse.error()))

    renderLogin()
    await user.type(screen.getByLabelText(/token/i), 'secret-token')
    await user.click(screen.getByRole('button', { name: /sign in/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/could not reach the server/i)
  })
})
