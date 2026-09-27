import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import App from './App'
import './index.css'

async function prepare() {
  if (import.meta.env.MODE === 'test') return
  // Use MSW until M2-106 serves the real API. Opt out with VITE_USE_MSW=false.
  if (import.meta.env.VITE_USE_MSW === 'false') return
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
