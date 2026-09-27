import {
  AlertTriangle,
  CheckCircle2,
  CirclePause,
  Loader2,
  type LucideIcon,
  XCircle,
} from 'lucide-react'

import type { AgentState, SystemStatus } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

type BadgeVariant = 'default' | 'ok' | 'warn' | 'err' | 'accent'

const systemMap: Record<SystemStatus, { label: string; icon: LucideIcon; variant: BadgeVariant }> =
  {
    running: { label: 'Running', icon: CheckCircle2, variant: 'ok' },
    paused: { label: 'Paused', icon: CirclePause, variant: 'warn' },
    degraded: { label: 'Degraded', icon: AlertTriangle, variant: 'err' },
  }

const agentMap: Record<AgentState, { label: string; icon: LucideIcon; variant: BadgeVariant }> = {
  idle: { label: 'Idle', icon: CheckCircle2, variant: 'default' },
  working: { label: 'Working', icon: Loader2, variant: 'accent' },
  paused: { label: 'Paused', icon: CirclePause, variant: 'warn' },
  error: { label: 'Error', icon: XCircle, variant: 'err' },
}

export function StatusPill({ status, className }: { status: SystemStatus; className?: string }) {
  const meta = systemMap[status]
  const Icon = meta.icon
  return (
    <Badge variant={meta.variant} className={cn('min-h-11 px-3', className)} role="status">
      <Icon className="h-4 w-4" aria-hidden />
      <span>{meta.label}</span>
    </Badge>
  )
}

export function AgentStateBadge({ state }: { state: AgentState }) {
  const meta = agentMap[state]
  const Icon = meta.icon
  return (
    <Badge variant={meta.variant} role="status">
      <Icon className={cn('h-3.5 w-3.5', state === 'working' && 'animate-spin')} aria-hidden />
      <span>{meta.label}</span>
    </Badge>
  )
}
