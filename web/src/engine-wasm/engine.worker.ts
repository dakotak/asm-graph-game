// Web Worker that runs the Go engine (WebAssembly). The page talks to it
// with plain JSON-RPC strings, exactly what it used to send over a
// WebSocket to `asmgraph web`; see ./workerSocket.ts.
//
//   page -> worker: {type: 'init', base}       where engine/ is hosted (first)
//                   string                     a JSON-RPC message
//   worker -> page: {type: 'ready'}            the engine is running
//                   {type: 'message', data}    a response or event
//                   {type: 'fatal', error}     the engine failed
import { loadEngine } from './host'

const ctx = self as unknown as {
  postMessage(m: unknown): void
  onmessage: ((e: MessageEvent<string | { type: 'init'; base: string }>) => void) | null
}

function fatal(e: unknown) {
  ctx.postMessage({ type: 'fatal', error: e instanceof Error ? e.message : String(e) })
}

// The page says where the engine files are (they live next to the app, in
// engine/), so the app can be hosted under any path.
ctx.onmessage = (first) => {
  const msg = first.data
  if (typeof msg === 'string' || msg.type !== 'init') return
  ctx.onmessage = null
  loadEngine({
    loadRuntime: () => import(/* @vite-ignore */ new URL('wasm_exec.js', msg.base).href),
    wasm: new URL('asmgraph.wasm', msg.base),
    onExit: () => fatal('the engine stopped'),
  }).then(
    (engine) => {
      engine.onEvent((n) => ctx.postMessage({ type: 'message', data: n }))
      ctx.onmessage = (e) => {
        if (typeof e.data !== 'string') return
        try {
          const resp = engine.handle(e.data)
          if (resp !== null) ctx.postMessage({ type: 'message', data: resp })
        } catch (err) {
          fatal(err)
        }
      }
      ctx.postMessage({ type: 'ready' })
    },
    (e) => fatal(e),
  )
}
