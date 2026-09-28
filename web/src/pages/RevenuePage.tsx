import { useState } from 'react'

import { useCreateRevenue, useRevenue } from '@/api/hooks'
import type { RevenueEntry, RevenueLine } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

const LINES: RevenueLine[] = ['ads', 'affiliate', 'agency', 'saas', 'sponsor']

const fieldClass =
  'min-h-11 rounded-md border border-[var(--border)] bg-[var(--bg)] px-3 text-sm text-[var(--text)]'

function totalsByLine(entries: RevenueEntry[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (const e of entries) {
    out[e.line] = (out[e.line] ?? 0) + e.amount
  }
  return out
}

export function RevenuePage() {
  const revenue = useRevenue()
  const create = useCreateRevenue()
  const entries = revenue.data?.entries ?? []
  const totals = totalsByLine(entries)

  const [line, setLine] = useState<RevenueLine>('affiliate')
  const [source, setSource] = useState('')
  const [amount, setAmount] = useState('')
  const [currency, setCurrency] = useState('USD')
  const [date, setDate] = useState(() => new Date().toISOString().slice(0, 10))
  const [note, setNote] = useState('')
  const [formError, setFormError] = useState<string | null>(null)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setFormError(null)
    const parsed = Number(amount)
    if (!source.trim() || !date || Number.isNaN(parsed)) {
      setFormError('Source, amount, and date are required.')
      return
    }
    try {
      await create.mutateAsync({
        line,
        source: source.trim(),
        amount: parsed,
        currency: currency.trim() || 'USD',
        date,
        note: note.trim() || undefined,
      })
      setSource('')
      setAmount('')
      setNote('')
    } catch {
      setFormError('Could not save entry.')
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <header>
        <h1 className="text-xl font-semibold">Revenue</h1>
        <p className="mt-1 text-sm text-[var(--muted)]">
          Ads (YouTube auto-pull when monetary scope is granted), plus manual affiliate, agency,
          SaaS, and sponsor entries. Affiliate income is entered manually — no dashboard scraping.
        </p>
      </header>

      {revenue.isLoading ? <p className="text-sm text-[var(--muted)]">Loading revenue…</p> : null}
      {revenue.isError ? (
        <p className="text-sm text-[var(--err)]" role="alert">
          Failed to load revenue.
        </p>
      ) : null}

      {!revenue.isLoading && !revenue.isError && entries.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>No entries yet</CardTitle>
            <CardDescription>
              Add a manual entry below, or wait for a YouTube ads pull once monetary analytics scope
              is authorized.
            </CardDescription>
          </CardHeader>
        </Card>
      ) : null}

      {entries.length > 0 ? (
        <section aria-label="Totals by line" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          {LINES.map((l) => (
            <Card key={l}>
              <CardHeader className="pb-2">
                <CardDescription className="capitalize">{l}</CardDescription>
                <CardTitle className="text-lg tabular-nums">
                  {(totals[l] ?? 0).toLocaleString(undefined, {
                    style: 'currency',
                    currency: 'USD',
                    maximumFractionDigits: 2,
                  })}
                </CardTitle>
              </CardHeader>
            </Card>
          ))}
        </section>
      ) : null}

      {entries.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>Entries</CardTitle>
            <CardDescription>Newest first</CardDescription>
          </CardHeader>
          <CardContent className="overflow-x-auto">
            <table className="w-full min-w-[36rem] text-left text-sm">
              <thead className="border-b border-[var(--border)] text-[var(--muted)]">
                <tr>
                  <th className="py-2 pr-3 font-medium">Date</th>
                  <th className="py-2 pr-3 font-medium">Line</th>
                  <th className="py-2 pr-3 font-medium">Source</th>
                  <th className="py-2 pr-3 font-medium">Amount</th>
                  <th className="py-2 font-medium">Note</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((row) => (
                  <tr key={row.id} className="border-b border-[var(--border)] last:border-0">
                    <td className="py-2 pr-3 tabular-nums">{row.date}</td>
                    <td className="py-2 pr-3">
                      <Badge variant="default">{row.line}</Badge>
                    </td>
                    <td className="py-2 pr-3 font-mono text-xs">{row.source}</td>
                    <td className="py-2 pr-3 tabular-nums">
                      {row.amount.toLocaleString(undefined, {
                        style: 'currency',
                        currency: row.currency || 'USD',
                      })}
                    </td>
                    <td className="py-2 text-[var(--muted)]">{row.note ?? ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </CardContent>
        </Card>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Add entry</CardTitle>
          <CardDescription>Manual affiliate / agency / SaaS / sponsor / Meta ads</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4 sm:grid-cols-2" onSubmit={onSubmit}>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Line</span>
              <select
                className={fieldClass}
                value={line}
                onChange={(ev) => setLine(ev.target.value as RevenueLine)}
              >
                {LINES.map((l) => (
                  <option key={l} value={l}>
                    {l}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Source</span>
              <input
                className={fieldClass}
                value={source}
                onChange={(ev) => setSource(ev.target.value)}
                placeholder="amazon-associates"
                required
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Amount</span>
              <input
                className={fieldClass}
                type="number"
                step="0.01"
                value={amount}
                onChange={(ev) => setAmount(ev.target.value)}
                required
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Currency</span>
              <input
                className={fieldClass}
                value={currency}
                onChange={(ev) => setCurrency(ev.target.value)}
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Date</span>
              <input
                className={fieldClass}
                type="date"
                value={date}
                onChange={(ev) => setDate(ev.target.value)}
                required
              />
            </label>
            <label className="flex flex-col gap-1.5 text-sm">
              <span>Note</span>
              <input
                className={fieldClass}
                value={note}
                onChange={(ev) => setNote(ev.target.value)}
              />
            </label>
            {formError ? (
              <p className="sm:col-span-2 text-sm text-[var(--err)]" role="alert">
                {formError}
              </p>
            ) : null}
            <div className="sm:col-span-2">
              <Button type="submit" disabled={create.isPending}>
                {create.isPending ? 'Saving…' : 'Save entry'}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
