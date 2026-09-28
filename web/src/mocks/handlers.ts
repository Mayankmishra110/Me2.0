import { http, HttpResponse } from 'msw'

import type { DecisionRequest, PauseRequest } from '@/api/types'

import { db, pushEvent } from './data'

export const handlers = [
  http.get('/api/health', () => HttpResponse.json(db.health)),

  http.get('/api/agents', () => HttpResponse.json({ agents: db.agents })),

  http.get('/api/jobs', ({ request }) => {
    const status = new URL(request.url).searchParams.get('status')
    const jobs = status ? db.jobs.filter((j) => j.status === status) : db.jobs
    return HttpResponse.json({ jobs })
  }),

  http.post('/api/jobs/:id/retry', ({ params }) => {
    const job = db.jobs.find((j) => j.id === params.id)
    if (!job) return HttpResponse.json({ error: 'not found' }, { status: 404 })
    job.status = 'queued'
    job.error = null
    job.updatedAt = new Date().toISOString()
    pushEvent('job.updated', { id: job.id, status: job.status })
    return HttpResponse.json({ ok: true, id: job.id })
  }),

  http.get('/api/approvals', ({ request }) => {
    const status = new URL(request.url).searchParams.get('status') ?? 'pending'
    const approvals = db.approvals
      .filter((a) => a.status === status)
      .map(({ id, title, channel, format, status: s, createdAt }) => ({
        id,
        title,
        channel,
        format,
        status: s,
        createdAt,
      }))
    return HttpResponse.json({ approvals })
  }),

  http.get('/api/approvals/:id', ({ params }) => {
    const item = db.approvals.find((a) => a.id === params.id)
    if (!item) return HttpResponse.json({ error: 'not found' }, { status: 404 })
    return HttpResponse.json(item)
  }),

  http.post('/api/approvals/:id/decision', async ({ params, request }) => {
    const item = db.approvals.find((a) => a.id === params.id)
    if (!item) return HttpResponse.json({ error: 'not found' }, { status: 404 })
    if (item.status !== 'pending') {
      return HttpResponse.json({ error: 'already decided' }, { status: 409 })
    }
    const body = (await request.json()) as DecisionRequest
    if (!body?.decision || !['approve', 'reject', 'redo'].includes(body.decision)) {
      return HttpResponse.json({ error: 'invalid decision' }, { status: 400 })
    }
    if (body.edits?.title) item.title = body.edits.title
    if (body.edits?.description) item.description = body.edits.description
    item.status =
      body.decision === 'approve' ? 'approved' : body.decision === 'reject' ? 'rejected' : 'redo'
    const decidedAt = new Date().toISOString()
    pushEvent('approval.decided', {
      id: item.id,
      decision: body.decision,
      note: body.note ?? null,
    })
    return HttpResponse.json({ ok: true, id: item.id, decision: body.decision, decidedAt })
  }),

  http.get('/api/content', ({ request }) => {
    const url = new URL(request.url)
    const channel = url.searchParams.get('channel')
    const stage = url.searchParams.get('stage')
    let items = db.content
    if (channel) items = items.filter((i) => i.channel === channel)
    if (stage) items = items.filter((i) => i.stage === stage)
    return HttpResponse.json({ items })
  }),

  http.get('/api/calendar', () => HttpResponse.json({ entries: [] })),
  http.get('/api/channels/:id/metrics', () =>
    HttpResponse.json({ channelId: 'unknown', range: '7d', points: [] }),
  ),
  http.get('/api/topics', () => HttpResponse.json({ topics: [] })),
  http.post('/api/topics', async ({ request }) => {
    const body = await request.json()
    return HttpResponse.json({ ok: true, topic: body }, { status: 201 })
  }),
  http.get('/api/builder', () => HttpResponse.json(db.builder)),
  http.get('/api/revenue', () => HttpResponse.json({ entries: db.revenue })),
  http.post('/api/revenue', async ({ request }) => {
    const body = (await request.json()) as {
      line: string
      source: string
      amount: number
      currency?: string
      date: string
      note?: string
    }
    const entry = {
      id: `rev-${db.revenue.length + 1}`,
      line: body.line,
      source: body.source,
      amount: body.amount,
      currency: body.currency ?? 'USD',
      date: body.date,
      note: body.note,
    }
    db.revenue = [entry as (typeof db.revenue)[number], ...db.revenue]
    return HttpResponse.json({ ok: true, entry }, { status: 201 })
  }),

  http.post('/api/pause', async ({ request }) => {
    const body = (await request.json()) as PauseRequest
    db.health.paused = true
    db.health.status = 'paused'
    if (body.scope === 'all') {
      for (const agent of db.agents) {
        if (agent.state === 'working' || agent.state === 'idle') agent.state = 'paused'
      }
    } else {
      const agent = db.agents.find((a) => a.id === body.scope)
      if (agent) agent.state = 'paused'
    }
    pushEvent('agent.state', { scope: body.scope, paused: true })
    return HttpResponse.json({ ok: true, paused: true, scope: body.scope })
  }),

  http.post('/api/resume', async ({ request }) => {
    const body = (await request.json()) as PauseRequest
    db.health.paused = false
    db.health.status = 'running'
    if (body.scope === 'all') {
      for (const agent of db.agents) {
        if (agent.state === 'paused') agent.state = 'idle'
      }
    }
    pushEvent('agent.state', { scope: body.scope, paused: false })
    return HttpResponse.json({ ok: true, paused: false, scope: body.scope })
  }),

  http.get('/api/events/stream', () => {
    const encoder = new TextEncoder()
    let tick = 0
    const stream = new ReadableStream({
      start(controller) {
        const hello = db.events[0] ?? {
          kind: 'alert' as const,
          at: new Date().toISOString(),
          payload: { message: 'SSE connected (mock)', level: 'info' },
        }
        controller.enqueue(encoder.encode(`data: ${JSON.stringify(hello)}\n\n`))
        const id = setInterval(() => {
          tick += 1
          const event = {
            kind: 'agent.state' as const,
            at: new Date().toISOString(),
            payload: { heartbeat: tick },
          }
          try {
            controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`))
          } catch {
            clearInterval(id)
          }
        }, 15_000)
      },
    })
    return new HttpResponse(stream, {
      headers: {
        'Content-Type': 'text/event-stream',
        Connection: 'keep-alive',
        'Cache-Control': 'no-cache',
      },
    })
  }),

  http.get('/media/:assetId', () => {
    // Tiny 1x1 PNG so <video>/<img> don't 404 in the mock.
    const png = Uint8Array.from(
      atob(
        'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
      ),
      (c) => c.charCodeAt(0),
    )
    return new HttpResponse(png, { headers: { 'Content-Type': 'image/png' } })
  }),

  http.post('/api/login', async () =>
    HttpResponse.json({ ok: true }, { headers: { 'Set-Cookie': 'session=mock; Path=/' } }),
  ),
]
