/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The engine is Go compiled to WebAssembly (scripts/build-wasm.sh writes it
// to public/engine/), so the app is entirely static: no backend at all.

/**
 * Content Security Policy for production builds (dev needs inline scripts
 * for hot reload). 'wasm-unsafe-eval' allows compiling the engine; nothing
 * is loaded from other origins.
 */
function csp(): Plugin {
  const policy = [
    "default-src 'self'",
    "script-src 'self' 'wasm-unsafe-eval'",
    "worker-src 'self'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self' data:",
    "connect-src 'self'",
    "object-src 'none'",
    "base-uri 'none'",
  ].join('; ')
  return {
    name: 'asmgraph-csp',
    apply: 'build',
    transformIndexHtml: (html) => html.replace('<head>', `<head>\n    <meta http-equiv="Content-Security-Policy" content="${policy}" />`),
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), csp()],
  // Relative asset URLs, so the build can be hosted under any path.
  base: './',
  worker: { format: 'es' },
  resolve: {
    alias: [
      // Monaco's CSS files are not covered by its package "exports" map.
      {
        find: /^monaco-esm\/(.*)$/,
        replacement: fileURLToPath(new URL('./node_modules/monaco-editor/esm/vs/$1', import.meta.url)),
      },
    ],
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    // The bundled puzzles live in the repository's examples/ directory.
    fs: { allow: ['..'] },
  },
  preview: { host: '127.0.0.1' },
  build: {
    target: 'es2022',
    chunkSizeWarningLimit: 4000,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    css: false,
  },
})
