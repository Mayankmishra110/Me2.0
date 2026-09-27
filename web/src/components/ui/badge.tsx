import { cva, type VariantProps } from 'class-variance-authority'
import * as React from 'react'

import { cn } from '@/lib/utils'

const badgeVariants = cva(
  'inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium',
  {
    variants: {
      variant: {
        default: 'border-[var(--border)] bg-[var(--surface)] text-[var(--text)]',
        ok: 'border-transparent bg-[color-mix(in_srgb,var(--ok)_20%,transparent)] text-[var(--ok)]',
        warn: 'border-transparent bg-[color-mix(in_srgb,var(--warn)_20%,transparent)] text-[var(--warn)]',
        err: 'border-transparent bg-[color-mix(in_srgb,var(--err)_20%,transparent)] text-[var(--err)]',
        accent:
          'border-transparent bg-[color-mix(in_srgb,var(--accent)_20%,transparent)] text-[var(--accent)]',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />
}
