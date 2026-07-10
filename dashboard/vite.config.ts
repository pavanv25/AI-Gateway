import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    // Proxy /v1/* to the gateway — avoids CORS for local dev.
    proxy: {
      '/v1': 'http://localhost:8080',
    },
  },
})
