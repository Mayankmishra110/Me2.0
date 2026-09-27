import { Check, RotateCcw, X } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { useApproval, useApprovalDecision, useApprovals } from '@/api/hooks'
import type { Decision } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

export function ApprovalsPage() {
  const list = useApprovals('pending')

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">Approvals</h1>
      {list.isLoading ? <p className="text-sm text-[var(--muted)]">Loading…</p> : null}
      {list.isError ? (
        <p className="text-sm text-[var(--err)]" role="alert">
          Failed to load approvals.
        </p>
      ) : null}
      {(list.data?.approvals.length ?? 0) === 0 && !list.isLoading ? (
        <p className="text-sm text-[var(--muted)]">No pending approvals.</p>
      ) : null}
      <ul className="flex flex-col gap-2">
        {(list.data?.approvals ?? []).map((item) => (
          <li key={item.id}>
            <Link
              to={`/approvals/${item.id}`}
              className="flex min-h-14 flex-col gap-1 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-3 hover:border-[var(--accent)]"
            >
              <span className="font-medium">{item.title}</span>
              <span className="text-xs text-[var(--muted)]">
                {item.channel} · {item.format}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function ApprovalDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const detail = useApproval(id)
  const decision = useApprovalDecision(id ?? '')
  const [note, setNote] = useState('')
  const busy = decision.isPending

  const submit = (value: Decision) => {
    if (busy || !id) return
    decision.mutate(
      { decision: value, note: value === 'redo' ? note || undefined : undefined },
      {
        onSuccess: () => {
          void navigate('/approvals')
        },
      },
    )
  }

  if (detail.isLoading) {
    return <p className="text-sm text-[var(--muted)]">Loading approval…</p>
  }
  if (detail.isError || !detail.data) {
    return (
      <p className="text-sm text-[var(--err)]" role="alert">
        Approval not found.
      </p>
    )
  }

  const item = detail.data
  const decided = item.status !== 'pending'

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button asChild variant="secondary">
          <Link to="/approvals">Back</Link>
        </Button>
        <Badge variant="accent">{item.channel}</Badge>
        <Badge>{item.format}</Badge>
        <Badge variant={item.status === 'pending' ? 'warn' : 'ok'}>{item.status}</Badge>
      </div>

      <h1 className="text-xl font-semibold">{item.title}</h1>
      <p className="text-sm text-[var(--muted)]">{item.description}</p>

      <div
        className={
          item.aspectRatio === '9:16'
            ? 'mx-auto w-full max-w-[280px] overflow-hidden rounded-lg border border-[var(--border)] bg-black'
            : 'w-full overflow-hidden rounded-lg border border-[var(--border)] bg-black'
        }
      >
        <video
          className="aspect-video w-full bg-black object-contain"
          controls
          poster={item.thumbnailUrl}
          src={item.mediaUrl}
          aria-label={`Preview for ${item.title}`}
        >
          <track kind="captions" />
        </video>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Compliance {item.compliance.score}</CardTitle>
          <CardDescription>Gate results</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {item.compliance.gates.map((gate) => (
            <div
              key={gate.id}
              className="flex min-h-11 items-start justify-between gap-3 rounded-md border border-[var(--border)] px-3 py-2 text-sm"
            >
              <div>
                <p className="font-medium">{gate.label}</p>
                <p className="text-[var(--muted)]">{gate.detail}</p>
              </div>
              <Badge variant={gate.pass ? 'ok' : 'err'}>{gate.pass ? 'Pass' : 'Fail'}</Badge>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Destinations</CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm">
          {item.destinations.map((d) => (
            <p key={`${d.platform}-${d.scheduledAt}`}>
              {d.platform} · {new Date(d.scheduledAt).toLocaleString()}
            </p>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Sources</CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm">
          {item.sources.map((s) => (
            <a
              key={s.url}
              href={s.url}
              className="block min-h-11 py-2 text-[var(--accent)] underline-offset-2 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              {s.title}
            </a>
          ))}
        </CardContent>
      </Card>

      <div className="flex flex-col gap-3 rounded-lg border border-[var(--border)] bg-[var(--surface)] p-3">
        <label htmlFor="redo-note" className="text-sm font-medium">
          Redo note
        </label>
        <textarea
          id="redo-note"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          disabled={busy || decided}
          className="min-h-20 w-full rounded-md border border-[var(--border)] bg-[var(--bg)] px-3 py-2 text-sm"
          placeholder="Optional note for Script agent"
        />
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            aria-label="Approve"
            disabled={busy || decided}
            onClick={() => submit('approve')}
          >
            <Check className="h-4 w-4" aria-hidden />
            Approve
          </Button>
          <Button
            type="button"
            variant="destructive"
            aria-label="Reject"
            disabled={busy || decided}
            onClick={() => submit('reject')}
          >
            <X className="h-4 w-4" aria-hidden />
            Reject
          </Button>
          <Button
            type="button"
            variant="secondary"
            aria-label="Redo"
            disabled={busy || decided}
            onClick={() => submit('redo')}
          >
            <RotateCcw className="h-4 w-4" aria-hidden />
            Redo
          </Button>
        </div>
        {decision.isError ? (
          <p className="text-sm text-[var(--err)]" role="alert">
            Decision failed. Try again.
          </p>
        ) : null}
        {decided ? (
          <p className="text-sm text-[var(--ok)]" role="status">
            Already decided: {item.status}
          </p>
        ) : null}
      </div>
    </div>
  )
}
