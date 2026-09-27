import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import * as React from 'react'

import { cn } from '@/lib/utils'

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-colors disabled:pointer-events-none disabled:opacity-50 min-h-11 min-w-11 px-4 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent)]',
  {
    variants: {
      variant: {
        default: 'bg-[var(--accent)] text-[#0B0D12] hover:opacity-90',
        secondary:
          'bg-[var(--surface)] text-[var(--text)] border border-[var(--border)] hover:bg-[var(--border)]',
        destructive: 'bg-[var(--err)] text-[#0B0D12] hover:opacity-90',
        ghost: 'hover:bg-[var(--border)] text-[var(--text)]',
        outline: 'border border-[var(--border)] bg-transparent hover:bg-[var(--surface)]',
      },
      size: {
        default: 'h-11 px-4',
        sm: 'h-11 px-3 text-sm',
        lg: 'h-12 px-6',
        icon: 'h-11 w-11 p-0',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />
    )
  },
)
Button.displayName = 'Button'
