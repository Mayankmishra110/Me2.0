import { ClipboardCheck, Home, MoreHorizontal, ScrollText, Wrench } from 'lucide-react'
import { NavLink } from 'react-router-dom'

import { cn } from '@/lib/utils'

const items = [
  { to: '/', label: 'Home', icon: Home, end: true },
  { to: '/approvals', label: 'Approvals', icon: ClipboardCheck },
  { to: '/pipeline', label: 'Pipeline', icon: MoreHorizontal, disabled: true },
  { to: '/builder', label: 'Builder', icon: Wrench },
  { to: '/logs', label: 'Logs', icon: ScrollText },
] as const

export function Sidebar() {
  return (
    <aside className="hidden min-h-screen w-56 shrink-0 border-r border-[var(--border)] bg-[var(--surface)] p-3 sm:block">
      <div className="mb-6 px-2 pt-2">
        <p className="text-lg font-semibold tracking-tight">Mayank 2.0</p>
        <p className="text-xs text-[var(--muted)]">Dashboard</p>
      </div>
      <nav aria-label="Primary" className="flex flex-col gap-1">
        {items.map((item) => {
          const Icon = item.icon
          if ('disabled' in item && item.disabled) {
            return (
              <span
                key={item.to}
                className="flex min-h-11 items-center gap-3 rounded-md px-3 text-sm text-[var(--muted)] opacity-60"
                aria-disabled="true"
              >
                <Icon className="h-4 w-4" aria-hidden />
                {item.label}
              </span>
            )
          }
          return (
            <NavLink
              key={item.to}
              to={item.to}
              end={'end' in item ? item.end : false}
              className={({ isActive }) =>
                cn(
                  'flex min-h-11 items-center gap-3 rounded-md px-3 text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-[color-mix(in_srgb,var(--accent)_18%,transparent)] text-[var(--accent)]'
                    : 'text-[var(--text)] hover:bg-[var(--border)]',
                )
              }
            >
              <Icon className="h-4 w-4" aria-hidden />
              {item.label}
            </NavLink>
          )
        })}
      </nav>
    </aside>
  )
}

const mobileTabs = [
  { to: '/', label: 'Home', icon: Home, end: true },
  { to: '/approvals', label: 'Approvals', icon: ClipboardCheck },
  { to: '/builder', label: 'Builder', icon: Wrench },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/more', label: 'More', icon: MoreHorizontal },
] as const

export function BottomTabs() {
  return (
    <nav
      aria-label="Primary mobile"
      className="fixed inset-x-0 bottom-0 z-40 flex border-t border-[var(--border)] bg-[var(--surface)] sm:hidden"
    >
      {mobileTabs.map((item) => {
        const Icon = item.icon
        return (
          <NavLink
            key={item.to}
            to={item.to}
            end={'end' in item ? item.end : false}
            className={({ isActive }) =>
              cn(
                'flex min-h-14 flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-medium',
                isActive ? 'text-[var(--accent)]' : 'text-[var(--muted)]',
              )
            }
          >
            <Icon className="h-5 w-5" aria-hidden />
            {item.label}
          </NavLink>
        )
      })}
    </nav>
  )
}
