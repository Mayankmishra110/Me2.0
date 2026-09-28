import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { ApprovalDetailPage, ApprovalsPage } from '@/pages/ApprovalsPage'
import { server } from '@/mocks/server'

function renderApprovals(path = '/approvals') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/approvals" element={<ApprovalsPage />} />
          <Route path="/approvals/:id" element={<ApprovalDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('Approvals decision flow', () => {
  it('lists pending approvals and posts approve via decision endpoint', async () => {
    const user = userEvent.setup()
    let decisionBody: unknown
    server.use(
      http.post('/api/approvals/:id/decision', async ({ request, params }) => {
        decisionBody = await request.json()
        return HttpResponse.json({
          ok: true,
          id: params.id,
          decision: 'approve',
          decidedAt: new Date().toISOString(),
        })
      }),
    )

    renderApprovals()
    expect(await screen.findByText(/AI can't read your screen/i)).toBeInTheDocument()
    await user.click(screen.getByText(/AI can't read your screen/i))

    expect(await screen.findByRole('button', { name: 'Approve' })).toBeInTheDocument()
    expect(screen.getByText(/Compliance/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/Preview for/i)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => {
      expect(decisionBody).toEqual({ decision: 'approve' })
    })
  })

  it('posts reject through the decision endpoint', async () => {
    const user = userEvent.setup()
    let body: unknown
    server.use(
      http.post('/api/approvals/:id/decision', async ({ request, params }) => {
        body = await request.json()
        return HttpResponse.json({
          ok: true,
          id: params.id,
          decision: 'reject',
          decidedAt: new Date().toISOString(),
        })
      }),
    )

    renderApprovals('/approvals/appr-1')
    await user.click(await screen.findByRole('button', { name: 'Reject' }))
    await waitFor(() => expect(body).toEqual({ decision: 'reject' }))
  })

  it('posts redo with note through the decision endpoint', async () => {
    const user = userEvent.setup()
    let body: unknown
    server.use(
      http.post('/api/approvals/:id/decision', async ({ request, params }) => {
        body = await request.json()
        return HttpResponse.json({
          ok: true,
          id: params.id,
          decision: 'redo',
          decidedAt: new Date().toISOString(),
        })
      }),
    )

    renderApprovals('/approvals/appr-2')
    await screen.findByRole('button', { name: 'Redo' })
    await user.type(screen.getByLabelText(/Redo note/i), 'Tighten hook')
    await user.click(screen.getByRole('button', { name: 'Redo' }))
    await waitFor(() => expect(body).toEqual({ decision: 'redo', note: 'Tighten hook' }))
  })

  it('disables decision buttons while a mutation is in flight (no double submit)', async () => {
    const user = userEvent.setup()
    let release!: () => void
    const hold = new Promise<void>((resolve) => {
      release = resolve
    })
    let calls = 0
    server.use(
      http.post('/api/approvals/:id/decision', async ({ params }) => {
        calls += 1
        await hold
        return HttpResponse.json({
          ok: true,
          id: params.id,
          decision: 'approve',
          decidedAt: new Date().toISOString(),
        })
      }),
    )

    renderApprovals('/approvals/appr-1')
    const approve = await screen.findByRole('button', { name: 'Approve' })
    await user.click(approve)

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Approve' })).toBeDisabled()
      expect(screen.getByRole('button', { name: 'Reject' })).toBeDisabled()
      expect(screen.getByRole('button', { name: 'Redo' })).toBeDisabled()
    })
    expect(calls).toBe(1)

    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(calls).toBe(1)

    release()
    cleanup()
  })
})
