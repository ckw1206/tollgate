import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// `npm run dev` serves the SPA on :5173 and proxies API calls to the Go server on :4000.
// Start the Go server with BASE_URL=http://localhost:5173: the browser's Origin header is
// :5173, and the CSRF check rejects state-changing requests whose Origin isn't BASE_URL.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/auth': 'http://localhost:4000',
      '/console': 'http://localhost:4000',
      '/v1': 'http://localhost:4000',
    },
  },
})
