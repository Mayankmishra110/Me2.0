import { useState } from 'react'
import type { FormEvent } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'

import { useAuthStatus, useLogin } from '@/api/hooks'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface LocationState {
  from?: { pathname: string }
}

export function LoginPage() {
  const [token, setToken] = useState('')
  const [error, setError] = useState<string | null>(null)
  const login = useLogin()
  const navigate = useNavigate()
  const location = useLocation()
  const auth = useAuthStatus()

  const redirectTo = (location.state as LocationState | null)?.from?.pathname ?? '/'

  // Already have a valid session (e.g. opened /login directly while logged
  // in) — don't make the user log in again.
  if (auth.isSuccess) {
    return <Navigate to={redirectTo} replace />
  }

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    try {
      await login.mutateAsync(token)
      navigate(redirectTo, { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setError('Incorrect token')
      } else if (err instanceof ApiError && err.status === 429) {
        setError('Too many attempts. Wait a minute and try again.')
      } else {
        setError('Could not reach the server. Check your connection and try again.')
      }
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--bg)] px-4 text-[var(--text)]">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Mayank 2.0</CardTitle>
          <CardDescription>Enter your dashboard token to continue.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4" noValidate>
            <div className="flex flex-col gap-2">
              <label htmlFor="token" className="text-sm font-medium">
                Token
              </label>
              <Input
                id="token"
                name="token"
                type="password"
                autoComplete="off"
                autoFocus
                value={token}
                onChange={(e) => setToken(e.target.value)}
                aria-describedby={error ? 'login-error' : undefined}
                aria-invalid={error ? true : undefined}
              />
            </div>
            {error ? (
              <p id="login-error" role="alert" className="text-sm text-[var(--err)]">
                {error}
              </p>
            ) : null}
            <Button type="submit" disabled={login.isPending || token.trim() === ''}>
              {login.isPending ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
