import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import type { BuilderResponse } from '@/api/types'
import { server } from '@/mocks/server'
import { BuilderPage } from '@/pages/BuilderPage'

const populated: BuilderResponse = {
  plans: [
    {
      id: 'plan-1',
      repo: 'configured-repo',
      prUrl: 'https://github.com/acme/widget/compare/main...build/phase-1',
      phases: [
        {
          id: '1',
          title: 'Phase 1',
          subphases: [
            { id: '1.1', title: 'Scaffold', status: 'gated' },
            { id: '1.2', title: 'API surface', status: 'running' },
          ],
        },
      ],
    },
  ],
  threads: [
    {
      id: 'implementer',
      repo: 'configured-repo',
      currentSubphase: '1.2',
      lastOutput: ['writing handlers…', 'tests green'],
      sessionCost: 12.5,
      sessionLimit: 100,
    },
    {
      id: 'auditor',
      repo: 'configured-repo',
      currentSubphase: '1.1',
      lastOutput: ['checking ARCHITECTURE.md'],
      sessionCost: 3,
      sessionLimit: 100,
    },
  ],
  audits: [
    {
      id: 'audit-1',
      repo: 'configured-repo',
      subphase: '1.1',
      verdict: 'pass',
      findings: ['Acceptance criteria covered'],
    },
  ],
}

function renderBuilder() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/builder']}>
        <Routes>
          <Route path="/builder" element={<BuilderPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('BuilderPage', () => {
  it('renders empty state when API returns no plans', async () => {
    server.use(
      http.get('/api/builder', () => HttpResponse.json({ plans: [], threads: [], audits: [] })),
    )
    renderBuilder()
    expect(await screen.findByRole('heading', { name: 'Builder' })).toBeInTheDocument()
    expect(await screen.findByText(/No active plans/i)).toBeInTheDocument()
    expect(screen.getByText(/No audits yet/i)).toBeInTheDocument()
  })

  it('renders plan tree, threads, audits, and PR link from mocked API', async () => {
    server.use(http.get('/api/builder', () => HttpResponse.json(populated)))
    renderBuilder()

    expect(
      await screen.findByRole('heading', { name: /Plan · configured-repo/i }),
    ).toBeInTheDocument()
    expect(screen.getByText('Scaffold')).toBeInTheDocument()
    expect(screen.getByText('API surface')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Open PR \/ compare/i })).toHaveAttribute(
      'href',
      populated.plans[0].prUrl!,
    )
    expect(screen.getByRole('heading', { name: 'Implementer' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Auditor' })).toBeInTheDocument()
    expect(screen.getByText(/writing handlers/i)).toBeInTheDocument()
    expect(screen.getByText(/Acceptance criteria covered/i)).toBeInTheDocument()
  })
})
