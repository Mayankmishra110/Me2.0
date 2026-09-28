import {
  CheckCircle2,
  CircleDashed,
  ExternalLink,
  Loader2,
  type LucideIcon,
  XCircle,
} from 'lucide-react'

import { useBuilder } from '@/api/hooks'
import type { BuilderSubphaseStatus, BuilderThread } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

const statusMeta: Record<
  BuilderSubphaseStatus,
  { label: string; icon: LucideIcon; variant: 'default' | 'ok' | 'warn' | 'err' | 'accent' }
> = {
  pending: { label: 'Pending', icon: CircleDashed, variant: 'default' },
  running: { label: 'Running', icon: Loader2, variant: 'accent' },
  pass: { label: 'Pass', icon: CheckCircle2, variant: 'ok' },
  fail: { label: 'Fail', icon: XCircle, variant: 'err' },
  gated: { label: 'Gated', icon: CheckCircle2, variant: 'ok' },
  blocked: { label: 'Blocked', icon: XCircle, variant: 'warn' },
}

function SubphaseStatus({ status }: { status: BuilderSubphaseStatus }) {
  const meta = statusMeta[status]
  const Icon = meta.icon
  return (
    <Badge variant={meta.variant} role="status">
      <Icon className={cn('h-3.5 w-3.5', status === 'running' && 'animate-spin')} aria-hidden />
      <span>{meta.label}</span>
    </Badge>
  )
}

function ThreadPane({ thread, title }: { thread: BuilderThread | undefined; title: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>
          {thread?.currentSubphase ? `Current: ${thread.currentSubphase}` : 'No active subphase'}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {!thread ? (
          <p className="text-sm text-[var(--muted)]">Thread idle.</p>
        ) : (
          <>
            <p className="text-xs text-[var(--muted)]">
              Repo <span className="font-medium text-[var(--text)]">{thread.repo}</span>
              {thread.sessionCost != null || thread.sessionLimit != null ? (
                <>
                  {' '}
                  · cost {thread.sessionCost ?? '—'}
                  {thread.sessionLimit != null ? ` / ${thread.sessionLimit}` : ''}
                </>
              ) : null}
            </p>
            <div
              className="max-h-48 overflow-auto rounded-md border border-[var(--border)] bg-[var(--bg)] p-3 font-mono text-xs leading-relaxed text-[var(--text)]"
              aria-label={`${title} output`}
            >
              {(thread.lastOutput ?? []).length === 0 ? (
                <p className="text-[var(--muted)]">No output yet.</p>
              ) : (
                <ul className="flex flex-col gap-1">
                  {thread.lastOutput.map((line, i) => (
                    <li key={`${i}-${line.slice(0, 24)}`}>{line}</li>
                  ))}
                </ul>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}

export function BuilderPage() {
  const builder = useBuilder()
  const plans = builder.data?.plans ?? []
  const threads = builder.data?.threads ?? []
  const audits = builder.data?.audits ?? []

  const implementer = threads.find((t) => t.id === 'implementer')
  const auditor = threads.find((t) => t.id === 'auditor')

  return (
    <div className="flex flex-col gap-6">
      <header>
        <h1 className="text-xl font-semibold">Builder</h1>
        <p className="mt-1 text-sm text-[var(--muted)]">
          Plan tree, Implementer / Auditor panes, audits, and PR links.
        </p>
      </header>

      {builder.isLoading ? <p className="text-sm text-[var(--muted)]">Loading builder…</p> : null}
      {builder.isError ? (
        <p className="text-sm text-[var(--err)]" role="alert">
          Failed to load builder state.
        </p>
      ) : null}

      {!builder.isLoading && !builder.isError && plans.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>No active plans</CardTitle>
            <CardDescription>
              Builder plans appear here once a configured repo has an approved plan. Until then this
              screen stays empty — nothing is invented client-side.
            </CardDescription>
          </CardHeader>
        </Card>
      ) : null}

      {plans.map((plan) => (
        <section key={plan.id} aria-labelledby={`plan-${plan.id}`} className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 id={`plan-${plan.id}`} className="text-lg font-semibold">
              Plan · {plan.repo}
            </h2>
            {plan.prUrl ? (
              <a
                href={plan.prUrl}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-h-11 items-center gap-2 text-sm text-[var(--accent)] underline-offset-2 hover:underline"
              >
                <ExternalLink className="h-4 w-4" aria-hidden />
                Open PR / compare
              </a>
            ) : (
              <span className="text-xs text-[var(--muted)]">PR link pending</span>
            )}
          </div>

          <ol className="flex flex-col gap-3">
            {plan.phases.map((phase) => (
              <li
                key={phase.id}
                className="rounded-md border border-[var(--border)] bg-[var(--surface)] p-3"
              >
                <p className="mb-2 text-sm font-medium">{phase.title}</p>
                <ul className="flex flex-col gap-2">
                  {phase.subphases.map((sp) => (
                    <li
                      key={sp.id}
                      className="flex min-h-11 flex-wrap items-center justify-between gap-2 rounded-md px-2 py-1"
                    >
                      <span className="text-sm">{sp.title}</span>
                      <SubphaseStatus status={sp.status} />
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ol>
        </section>
      ))}

      <section aria-labelledby="threads-heading" className="flex flex-col gap-3">
        <h2 id="threads-heading" className="text-lg font-semibold">
          Threads
        </h2>
        <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
          <ThreadPane title="Implementer" thread={implementer} />
          <ThreadPane title="Auditor" thread={auditor} />
        </div>
      </section>

      <section aria-labelledby="audits-heading" className="flex flex-col gap-3">
        <h2 id="audits-heading" className="text-lg font-semibold">
          Audit findings
        </h2>
        {audits.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">No audits yet.</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {audits.map((audit) => (
              <li
                key={audit.id}
                className="rounded-md border border-[var(--border)] bg-[var(--surface)] p-3"
              >
                <div className="mb-2 flex flex-wrap items-center gap-2">
                  <p className="text-sm font-medium">
                    {audit.repo} · {audit.subphase}
                  </p>
                  <Badge variant={audit.verdict === 'pass' ? 'ok' : 'err'} role="status">
                    {audit.verdict === 'pass' ? (
                      <CheckCircle2 className="h-3.5 w-3.5" aria-hidden />
                    ) : (
                      <XCircle className="h-3.5 w-3.5" aria-hidden />
                    )}
                    <span>{audit.verdict === 'pass' ? 'Pass' : 'Fail'}</span>
                  </Badge>
                </div>
                {audit.findings.length === 0 ? (
                  <p className="text-sm text-[var(--muted)]">No findings.</p>
                ) : (
                  <ul className="list-disc space-y-1 pl-5 text-sm">
                    {audit.findings.map((f) => (
                      <li key={f}>{f}</li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
