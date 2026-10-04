import { Link } from 'react-router-dom'

export function MorePage() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="text-xl font-semibold">More</h1>
      <p className="text-sm text-[var(--muted)]">
        Pipeline, Calendar, Channels, and Settings arrive in later tickets.
      </p>
      <Link className="min-h-11 text-[var(--accent)] underline-offset-2 hover:underline" to="/logs">
        Open Logs
      </Link>
    </div>
  )
}
