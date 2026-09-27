import type { StreamEvent } from '@/api/types'
import { Badge } from '@/components/ui/badge'

export function LogsPage({ events }: { events: StreamEvent[] }) {
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">Logs</h1>
      <p className="text-sm text-[var(--muted)]">Live event stream from `/api/events/stream`.</p>
      {events.length === 0 ? (
        <p className="text-sm text-[var(--muted)]">Waiting for events…</p>
      ) : null}
      <ol className="flex flex-col gap-2 font-mono text-xs">
        {events.map((event, idx) => (
          <li
            key={`${event.at}-${event.kind}-${idx}`}
            className="min-h-11 rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 py-2"
          >
            <div className="mb-1 flex flex-wrap items-center gap-2">
              <Badge variant="accent">{event.kind}</Badge>
              <span className="text-[var(--muted)]">{new Date(event.at).toLocaleTimeString()}</span>
            </div>
            <pre className="overflow-x-auto whitespace-pre-wrap break-all text-[var(--text)]">
              {JSON.stringify(event.payload)}
            </pre>
          </li>
        ))}
      </ol>
    </div>
  )
}
