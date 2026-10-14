import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import './index.css'

async function prepare() {
  if (import.meta.env.MODE === 'test') return
  // MSW is a dev-only convenience: it must never run against a production build,
  // which is what serves the real dashboard over the real API (M2-106). Vite sets
  // `DEV` true for `vite`/`vite dev` and false for `vite build`'s output, so this
  // can't be forgotten the way a build-time opt-out env var was (M2-129).
  if (!import.meta.env.DEV) return
  const { worker } = await import('./mocks/browser')
  await worker.start({
    onUnhandledRequest: 'bypass',
    quiet: true,
  })
}

void prepare().then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
})
