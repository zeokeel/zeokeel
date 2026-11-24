import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The gateway owns the same origin in production (`go:embed web/dist`), so the
// client never needs CORS: in dev the Vite server proxies /api to the mock
// server (or to a running gateway via ZK_API_URL). See docs/TRANSPORT.md §3.2.
const apiTarget = process.env.ZK_API_URL ?? 'http://127.0.0.1:8788';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5178,
    strictPort: true,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: false,
      },
      // The WS control plane (docs/TRANSPORT.md §4.1). In production the
      // gateway serves the bundle itself, so there is no proxy at all; in dev
      // this is what makes `npm run dev` point at the mock (or a real gateway).
      // The fragment→cookie exchange (§3.1). Same origin in production (the
      // gateway serves the bundle), proxied here so dev has no CORS exception.
      '/auth': {
        target: apiTarget,
        changeOrigin: false,
      },
      '/ws': {
        target: apiTarget,
        ws: true,
        changeOrigin: false,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    css: false,
    // Vitest's 5 s default is a load test, not a correctness test: on a busy
    // machine (or CI's 2 vCPU) component tests that import antd/antd X took
    // 5–37 s and 20 of them failed as "timed out" with no assertion involved
    // (AUDIT-2026-11-24 C). 20 s still fails a genuinely hung test quickly.
    testTimeout: 20_000,
    hookTimeout: 20_000,
    // Cap the workers: these suites mount antd/antd X in jsdom, and the
    // timing-sensitive ones (scroll restoration, the app smoke) passed alone but
    // failed inside a swarm of workers on a big machine — a scheduling artefact,
    // not an assertion. Two workers keeps the suite under 2 minutes while
    // matching what a 2 vCPU CI runner does anyway.
    maxWorkers: 2,
  },
});