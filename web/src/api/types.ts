/** Types matching SPEC §4 endpoint shapes (minimal JSON until M2-106 lands). */

export type SystemStatus = 'running' | 'paused' | 'degraded'
export type AgentState = 'idle' | 'working' | 'paused' | 'error'
export type Decision = 'approve' | 'reject' | 'redo'
export type ApprovalStatus = 'pending' | 'approved' | 'rejected' | 'redo'
export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'dead'
export type ContentFormat = 'short' | 'long'
export type ContentStage =
  | 'topic'
  | 'research'
  | 'script'
  | 'compliance'
  | 'voice'
  | 'visuals'
  | 'render'
  | 'approval'
  | 'scheduled'
  | 'published'

export type SseEventKind =
  'job.updated' | 'approval.created' | 'approval.decided' | 'agent.state' | 'alert'

export interface HealthResponse {
  ok: boolean
  version: string
  paused: boolean
  status: SystemStatus
  claudeUsageLimitHit: boolean
}

export interface Agent {
  id: string
  name: string
  state: AgentState
  currentJob: string | null
  lastSuccess: string | null
  nextRun: string | null
}

export interface AgentsResponse {
  agents: Agent[]
}

export interface Job {
  id: string
  type: string
  status: JobStatus
  channel: string | null
  error: string | null
  updatedAt: string
}

export interface JobsResponse {
  jobs: Job[]
}

export interface ComplianceGate {
  id: string
  label: string
  pass: boolean
  detail: string
}

export interface ApprovalDestination {
  platform: string
  scheduledAt: string
}

export interface ApprovalSource {
  title: string
  url: string
}

export interface ApprovalSummary {
  id: string
  title: string
  channel: string
  format: ContentFormat
  status: ApprovalStatus
  createdAt: string
}

export interface ApprovalDetail extends ApprovalSummary {
  description: string
  tags: string[]
  thumbnailUrl: string
  mediaUrl: string
  aspectRatio: '9:16' | '16:9'
  destinations: ApprovalDestination[]
  compliance: { gates: ComplianceGate[]; score: string }
  sources: ApprovalSource[]
}

export interface ApprovalsListResponse {
  approvals: ApprovalSummary[]
}

export interface DecisionRequest {
  decision: Decision
  note?: string
  edits?: { title?: string; description?: string }
}

export interface DecisionResponse {
  ok: boolean
  id: string
  decision: Decision
  decidedAt: string
}

export interface ContentItem {
  id: string
  channel: string
  stage: ContentStage
  format: ContentFormat
  title: string
  updatedAt: string
}

export interface ContentResponse {
  items: ContentItem[]
}

/** Builder dashboard shapes for GET /api/builder (SPEC §4: plans, threads, audits). */
export type BuilderSubphaseStatus = 'pending' | 'running' | 'pass' | 'fail' | 'gated' | 'blocked'

export interface BuilderSubphase {
  id: string
  title: string
  status: BuilderSubphaseStatus
}

export interface BuilderPhase {
  id: string
  title: string
  subphases: BuilderSubphase[]
}

export interface BuilderPlan {
  id: string
  /** Configured repo name from the API — never hardcode product repos in UI. */
  repo: string
  phases: BuilderPhase[]
  prUrl: string | null
}

export interface BuilderThread {
  id: 'implementer' | 'auditor'
  repo: string
  currentSubphase: string | null
  lastOutput: string[]
  sessionCost: number | null
  sessionLimit: number | null
}

export interface BuilderAudit {
  id: string
  repo: string
  subphase: string
  verdict: 'pass' | 'fail'
  findings: string[]
}

export interface BuilderResponse {
  plans: BuilderPlan[]
  threads: BuilderThread[]
  audits: BuilderAudit[]
}

export interface PauseRequest {
  scope: 'all' | string
}

export interface PauseResponse {
  ok: boolean
  paused: boolean
  scope: string
}

export interface StreamEvent {
  kind: SseEventKind
  at: string
  payload: Record<string, unknown>
}

export const TODAY_TARGETS = { shorts: 8, long: 2 } as const
