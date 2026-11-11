import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/600.css'
import './index.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { controller } from './services/controller'
import { useSim } from './state/sim'
import { useWorkspace } from './state/workspace'

// Debug handle for development and end-to-end tests (not in production builds).
if (import.meta.env.DEV) {
  ;(window as unknown as Record<string, unknown>).__asmgraph = { controller, useSim, useWorkspace }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
