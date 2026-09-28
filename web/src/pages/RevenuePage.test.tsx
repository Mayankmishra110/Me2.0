import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import type { RevenueResponse } from '@/api/types'
import { server } from '@/mocks/server'
import { RevenuePage } from '@/pages/RevenuePage'

const populated: RevenueResponse = {
  entries: [
    {
      id: 'r1',
      line: 'ads',
      source: 'youtube:yt-money-en:2026-09-28:7d',
      amount: 18.5,
      currency: 'USD',
      date: '2026-09-28',
      note: 'auto-pulled',
    },
    {
      id: 'r2',
      line: 'affiliate',
      source: 'amazon-associates',
      amount: 42,
      currency: 'USD',
      date: '2026-09-20',
      note: 'sept payout',
    },
  ],
}

function renderRevenue() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/revenue']}>
        <Routes>
          <Route path="/revenue" element={<RevenuePage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('RevenuePage', () => {
  it('renders empty state when API returns no entries', async () => {
    server.use(http.get('/api/revenue', () => HttpResponse.json({ entries: [] })))
    renderRevenue()
    expect(await screen.findByRole('heading', { name: 'Revenue' })).toBeInTheDocument()
    expect(await screen.findByText(/No entries yet/i)).toBeInTheDocument()
  })

  it('renders totals and entry table from mocked API', async () => {
    server.use(http.get('/api/revenue', () => HttpResponse.json(populated)))
    renderRevenue()
    expect(await screen.findByRole('heading', { name: 'Entries' })).toBeInTheDocument()
    expect(screen.getByText('amazon-associates')).toBeInTheDocument()
    expect(screen.getByText(/auto-pulled/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Totals by line')).toBeInTheDocument()
  })
})
