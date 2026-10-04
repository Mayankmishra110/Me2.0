import { emitUnauthorized } from './authEvents'

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export interface ApiFetchInit extends RequestInit {
  /**
   * Set on the login call itself: a 401 there means "wrong token", which the
   * login form already surfaces inline. It must not also fire the global
   * unauthorized event (that would be a same-page no-op redirect, but it's
   * not what a failed login attempt means).
   */
  skipAuthEvent?: boolean
}

export async function apiFetch<T>(path: string, init?: ApiFetchInit): Promise<T> {
  const { skipAuthEvent, ...requestInit } = init ?? {}
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: {
      Accept: 'application/json',
      ...(requestInit.body ? { 'Content-Type': 'application/json' } : {}),
      ...requestInit.headers,
    },
    ...requestInit,
  })

  if (!res.ok) {
    if (res.status === 401 && !skipAuthEvent) {
      emitUnauthorized()
    }
    const text = await res.text().catch(() => '')
    throw new ApiError(res.status, text || res.statusText)
  }

  if (res.status === 204) {
    return undefined as T
  }

  return (await res.json()) as T
}
