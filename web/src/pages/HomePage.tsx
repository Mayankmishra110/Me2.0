import { AlertTriangle } from 'lucide-react'

import { useAgents, useContent, useHealth, useJobs } from '@/api/hooks'
import { TODAY_TARGETS } from '@/api/types'
import { AgentStateBadge } from '@/components/StatusPill'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

function isToday(iso: string) {
  const d = new Date(iso)
  const t = new Date()
  return (
    d.getFullYear() === t.getFullYear() &&
    d.getMonth() === t.getMonth() &&
    d.getDate() === t.getDate()
  )
}

export function HomePage() {
  const agents = useAgents()
  const content = useContent({ stage: 'published' })
  const deadJobs = useJobs('dead')
  const health = useHealth()

  const publishedToday = (content.data?.items ?? []).filter((i) => isToday(i.updatedAt))
  const shorts = publishedToday.filter((i) => i.format === 'short').length
  const long = publishedToday.filter((i) => i.format === 'long').length

  const alerts: { id: string; message: string }[] = []
  for (const job of deadJobs.data?.jobs ?? []) {
    alerts.push({ id: job.id, message: `Dead job ${job.type}: ${job.error ?? 'unknown error'}` })
  }
  if (health.data?.status === 'degraded') {
    alerts.push({ id: 'degraded', message: 'System status is degraded' })
  }
  if (health.data?.claudeUsageLimitHit) {
    alerts.push({ id: 'claude', message: 'Claude usage limit hit' })
  }

  return (
    <div className="flex flex-col gap-6">
      <section aria-labelledby="today-heading">
        <h1 id="today-heading" className="mb-3 text-xl font-semibold">
          Today
        </h1>
        <div className="grid grid-cols-1 gap-3 min-[360px]:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Shorts</CardTitle>
              <CardDescription>Published vs target</CardDescription>
            </CardHeader>
            <CardContent className="text-2xl font-semibold">
              {shorts} / {TODAY_TARGETS.shorts}
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle>Long</CardTitle>
              <CardDescription>Published vs target</CardDescription>
            </CardHeader>
            <CardContent className="text-2xl font-semibold">
              {long} / {TODAY_TARGETS.long}
            </CardContent>
          </Card>
        </div>
      </section>

      <section aria-labelledby="alerts-heading">
        <h2 id="alerts-heading" className="mb-3 text-lg font-semibold">
          Alerts
        </h2>
        {alerts.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">No alerts.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {alerts.map((a) => (
              <li
                key={a.id}
                className="flex min-h-11 items-start gap-2 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-[var(--warn)]" aria-hidden />
                <span>{a.message}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="agents-heading">
        <h2 id="agents-heading" className="mb-3 text-lg font-semibold">
          Agents
        </h2>
        {agents.isLoading ? <p className="text-sm text-[var(--muted)]">Loading agents…</p> : null}
        {agents.isError ? (
          <p className="text-sm text-[var(--err)]" role="alert">
            Failed to load agents.
          </p>
        ) : null}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {(agents.data?.agents ?? []).map((agent) => (
            <Card key={agent.id}>
              <CardHeader className="flex-row items-start justify-between gap-2 space-y-0">
                <div>
                  <CardTitle>{agent.name}</CardTitle>
                  <CardDescription className="mt-1 font-mono text-xs">{agent.id}</CardDescription>
                </div>
                <AgentStateBadge state={agent.state} />
              </CardHeader>
              <CardContent className="space-y-1 text-sm text-[var(--muted)]">
                <p>Job: {agent.currentJob ?? '—'}</p>
                <p>
                  Last success:{' '}
                  {agent.lastSuccess ? new Date(agent.lastSuccess).toLocaleString() : '—'}
                </p>
                <p>Next run: {agent.nextRun ? new Date(agent.nextRun).toLocaleString() : '—'}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>
    </div>
  )
}
