/**
 * Tiny pub/sub so any apiFetch call can announce "the session is gone" and a
 * component mounted inside the router (which has useNavigate) can react by
 * sending the user to /login. Decoupled from React Query so it fires even for
 * calls made outside a query/mutation (e.g. imperative fetches).
 */
type Listener = () => void

const listeners = new Set<Listener>()

export function onUnauthorized(cb: Listener): () => void {
  listeners.add(cb)
  return () => listeners.delete(cb)
}

export function emitUnauthorized(): void {
  for (const cb of listeners) cb()
}
