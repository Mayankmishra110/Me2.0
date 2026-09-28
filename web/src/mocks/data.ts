import type {
  Agent,
  ApprovalDetail,
  BuilderResponse,
  ContentItem,
  HealthResponse,
  Job,
  StreamEvent,
} from '@/api/types'

const now = Date.now()

export const db: {
  health: HealthResponse
  agents: Agent[]
  jobs: Job[]
  content: ContentItem[]
  approvals: ApprovalDetail[]
  events: StreamEvent[]
  builder: BuilderResponse
} = {
  health: {
    ok: true,
    version: '0.1.0-mock',
    paused: false,
    status: 'running',
    claudeUsageLimitHit: false,
  },
  agents: [
    {
      id: 'scout',
      name: 'Scout',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 3_600_000).toISOString(),
      nextRun: new Date(now + 1_800_000).toISOString(),
    },
    {
      id: 'research',
      name: 'Research',
      state: 'working',
      currentJob: 'research.brief',
      lastSuccess: new Date(now - 7_200_000).toISOString(),
      nextRun: null,
    },
    {
      id: 'script',
      name: 'Script',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 5_400_000).toISOString(),
      nextRun: new Date(now + 3_600_000).toISOString(),
    },
    {
      id: 'compliance',
      name: 'Compliance',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 4_000_000).toISOString(),
      nextRun: null,
    },
    {
      id: 'voice',
      name: 'Voice',
      state: 'paused',
      currentJob: null,
      lastSuccess: new Date(now - 86_400_000).toISOString(),
      nextRun: null,
    },
    {
      id: 'visuals',
      name: 'Visuals',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 9_000_000).toISOString(),
      nextRun: new Date(now + 7_200_000).toISOString(),
    },
    {
      id: 'render',
      name: 'Render',
      state: 'working',
      currentJob: 'render.short',
      lastSuccess: new Date(now - 2_000_000).toISOString(),
      nextRun: null,
    },
    {
      id: 'publish-youtube',
      name: 'Publisher · YouTube',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 12_000_000).toISOString(),
      nextRun: new Date(now + 10_800_000).toISOString(),
    },
    {
      id: 'analytics',
      name: 'Analytics',
      state: 'error',
      currentJob: null,
      lastSuccess: new Date(now - 172_800_000).toISOString(),
      nextRun: new Date(now + 600_000).toISOString(),
    },
    {
      id: 'blog',
      name: 'Blog',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 50_000_000).toISOString(),
      nextRun: new Date(now + 86_400_000).toISOString(),
    },
    {
      id: 'builder-implementer',
      name: 'Builder Implementer',
      state: 'working',
      currentJob: 'builder.implement',
      lastSuccess: new Date(now - 1_000_000).toISOString(),
      nextRun: null,
    },
    {
      id: 'builder-auditor',
      name: 'Builder Auditor',
      state: 'idle',
      currentJob: null,
      lastSuccess: new Date(now - 800_000).toISOString(),
      nextRun: null,
    },
  ],
  jobs: [
    {
      id: 'job-dead-1',
      type: 'analytics.pull',
      status: 'dead',
      channel: null,
      error: 'YouTube Analytics quota exhausted',
      updatedAt: new Date(now - 900_000).toISOString(),
    },
    {
      id: 'job-run-1',
      type: 'render.short',
      status: 'running',
      channel: 'yt-ai-en',
      error: null,
      updatedAt: new Date(now - 120_000).toISOString(),
    },
  ],
  content: [
    {
      id: 'c1',
      channel: 'yt-ai-en',
      stage: 'published',
      format: 'short',
      title: 'AI screen myth',
      updatedAt: new Date(now - 3_600_000).toISOString(),
    },
    {
      id: 'c2',
      channel: 'yt-money-en',
      stage: 'published',
      format: 'short',
      title: 'Side hustle #1',
      updatedAt: new Date(now - 2_000_000).toISOString(),
    },
    {
      id: 'c3',
      channel: 'yt-ai-hi',
      stage: 'published',
      format: 'short',
      title: 'AI tools short',
      updatedAt: new Date(now - 1_500_000).toISOString(),
    },
    {
      id: 'c4',
      channel: 'yt-ai-en',
      stage: 'published',
      format: 'long',
      title: 'Deep dive: free AI APIs',
      updatedAt: new Date(now - 8_000_000).toISOString(),
    },
    {
      id: 'c5',
      channel: 'yt-money-hi',
      stage: 'scheduled',
      format: 'short',
      title: 'Hustle tip',
      updatedAt: new Date(now - 400_000).toISOString(),
    },
  ],
  approvals: [
    {
      id: 'appr-1',
      title: "AI can't read your screen… right?",
      channel: 'yt-ai-en',
      format: 'short',
      status: 'pending',
      createdAt: new Date(now - 600_000).toISOString(),
      description: 'A 38s myth-vs-fact Short about screen reading claims.',
      tags: ['ai', 'myths', 'shorts'],
      thumbnailUrl: '/media/appr-1-thumb.jpg',
      mediaUrl: '/media/appr-1.mp4',
      aspectRatio: '9:16',
      destinations: [
        { platform: 'youtube', scheduledAt: new Date(now + 3_600_000).toISOString() },
        { platform: 'instagram', scheduledAt: new Date(now + 3_600_000).toISOString() },
      ],
      compliance: {
        score: '9/9',
        gates: [
          { id: 'originality', label: 'Originality', pass: true, detail: 'Embedding distance OK' },
          { id: 'policy', label: 'Platform policy', pass: true, detail: 'No prohibited claims' },
          { id: 'sources', label: 'Sources cited', pass: true, detail: '4 sources attached' },
        ],
      },
      sources: [
        { title: 'OpenAI docs', url: 'https://platform.openai.com/docs' },
        { title: 'Google AI blog', url: 'https://blog.google/technology/ai/' },
      ],
    },
    {
      id: 'appr-2',
      title: 'Free tools that replace a $200/mo stack',
      channel: 'yt-money-en',
      format: 'long',
      status: 'pending',
      createdAt: new Date(now - 1_200_000).toISOString(),
      description: 'Long-form walkthrough of free-tier tooling for solopreneurs.',
      tags: ['money', 'tools', 'long'],
      thumbnailUrl: '/media/appr-2-thumb.jpg',
      mediaUrl: '/media/appr-2.mp4',
      aspectRatio: '16:9',
      destinations: [{ platform: 'youtube', scheduledAt: new Date(now + 7_200_000).toISOString() }],
      compliance: {
        score: '8/9',
        gates: [
          { id: 'originality', label: 'Originality', pass: true, detail: 'Pass' },
          {
            id: 'claims',
            label: 'Income claims',
            pass: false,
            detail: 'Softened dollar figures after rewrite',
          },
          { id: 'policy', label: 'Platform policy', pass: true, detail: 'Pass' },
        ],
      },
      sources: [{ title: 'Internal research brief', url: 'https://example.local/brief/appr-2' }],
    },
  ],
  events: [],
  // Default empty — matches live GET /api/builder stub until builder jobs land.
  // Tests override with server.use(...) for populated fixtures.
  builder: { plans: [], threads: [], audits: [] },
}

export function pushEvent(kind: StreamEvent['kind'], payload: Record<string, unknown>) {
  const event: StreamEvent = {
    kind,
    at: new Date().toISOString(),
    payload,
  }
  db.events.unshift(event)
  if (db.events.length > 200) db.events.length = 200
  return event
}

export function resetDb() {
  db.health.paused = false
  db.health.status = 'running'
  for (const a of db.agents) {
    if (a.state === 'paused' && a.id !== 'voice') a.state = 'idle'
  }
  for (const a of db.approvals) {
    if (a.id === 'appr-1' || a.id === 'appr-2') a.status = 'pending'
  }
  db.events.length = 0
}
