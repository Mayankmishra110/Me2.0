import { useApprovals, useHealth, usePauseAll } from '@/api/hooks'
import type { SseConnectionState } from '@/api/sse'
import { StatusPill } from '@/components/StatusPill'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'

export function TopBar({ live }: { live: SseConnectionState }) {
  const health = useHealth()
  const approvals = useApprovals('pending')
  const pause = usePauseAll()
  const pending = approvals.data?.approvals.length ?? 0
  const status = health.data?.status ?? 'degraded'
  const paused = health.data?.paused ?? false

  return (
    <header className="sticky top-0 z-30 flex flex-wrap items-center gap-3 border-b border-[var(--border)] bg-[var(--bg)]/95 px-3 py-2 backdrop-blur sm:px-4">
      <StatusPill status={status} />
      <Badge variant="default" className="min-h-11 gap-2 px-3" aria-live="polite">
        <span
          className={
            live === 'live'
              ? 'h-2 w-2 rounded-full bg-[var(--ok)]'
              : live === 'connecting'
                ? 'h-2 w-2 rounded-full bg-[var(--warn)]'
                : 'h-2 w-2 rounded-full bg-[var(--err)]'
          }
          aria-hidden
        />
        <span className="capitalize">{live === 'live' ? 'Live' : live}</span>
      </Badge>
      <Badge variant="accent" className="min-h-11 px-3">
        {pending} pending
      </Badge>
      {health.data?.claudeUsageLimitHit ? (
        <Badge variant="warn" className="min-h-11 px-3">
          Claude limit hit
        </Badge>
      ) : null}
      <div className="ml-auto flex items-center gap-3">
        <label htmlFor="pause-all" className="flex min-h-11 items-center gap-2 text-sm">
          <span>Pause all</span>
          <Switch
            id="pause-all"
            checked={paused}
            disabled={pause.isPending}
            onCheckedChange={(checked) => {
              if (checked) pause.mutate({ scope: 'all' })
            }}
            aria-label="Pause all agents"
          />
        </label>
        {pause.isError ? (
          <Button type="button" variant="secondary" onClick={() => pause.mutate({ scope: 'all' })}>
            Retry pause
          </Button>
        ) : null}
      </div>
    </header>
  )
}
