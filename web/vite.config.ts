import { rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vitest/config'

const rootDir = path.dirname(fileURLToPath(import.meta.url))

// MSW never runs in a production build (main.tsx gates its start on
// `import.meta.env.DEV`, M2-129) — drop the mock worker script that Vite's
// publicDir copy would otherwise still put in dist/ and the embedded binary,
// so there's no inert MSW asset to ship at all.
function dropMswAsset(): Plugin {
  return {
    name: 'drop-msw-asset',
    apply: 'build',
    async closeBundle() {
      await rm(path.resolve(rootDir, 'dist/mockServiceWorker.js'), { force: true })
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), dropMswAsset()],
  resolve: {
    alias: {
      '@': path.resolve(rootDir, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/media': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: 'dist',
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: true,
  },
})
