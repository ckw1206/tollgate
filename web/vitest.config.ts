import { defineConfig } from 'vitest/config'

// Vitest-only config, kept separate from vite.config.ts so the app build (vite)
// and the test runner (vitest) never share a config file. Pure-function unit
// tests only — no jsdom: the helpers under test are browser-agnostic.
export default defineConfig({
  test: {
    environment: 'node',
  },
})
