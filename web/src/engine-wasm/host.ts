// Loads the Go engine compiled to WebAssembly (cmd/asmgraph-wasm) and
// exposes its JSON-RPC entry points. This works in a Web Worker (the IDE)
// and in Node (tests); it has no DOM dependencies.

/** The engine's global API, installed by cmd/asmgraph-wasm. */
export interface WasmEngine {
  /** Handles one JSON-RPC message; returns the response or null. */
  handle(message: string): string | null
  /** Registers the listener for JSON-RPC event notifications. */
  onEvent(fn: (notification: string) => void): void
  protocolVersion: string
}

interface GoRuntime {
  importObject: WebAssembly.Imports
  run(instance: WebAssembly.Instance): Promise<void>
}

type Globals = typeof globalThis & {
  Go?: new () => GoRuntime
  asmgraphEngine?: WasmEngine
  asmgraphEngineReady?: () => void
}

export interface LoadOptions {
  /** Loads Go's wasm_exec.js, which defines globalThis.Go. */
  loadRuntime: () => Promise<unknown>
  /** The compiled engine: a URL to fetch, or the bytes. */
  wasm: string | URL | BufferSource
  /** Called if the Go program exits (it should run forever). */
  onExit?: () => void
}

/** Loads and starts the engine. Only one engine may run per global scope. */
export async function loadEngine(opts: LoadOptions): Promise<WasmEngine> {
  const g = globalThis as Globals
  await opts.loadRuntime()
  if (!g.Go) throw new Error('wasm_exec.js did not define Go')
  const go = new g.Go()
  let instance: WebAssembly.Instance
  if (typeof opts.wasm === 'string' || opts.wasm instanceof URL) {
    const resp = await fetch(opts.wasm)
    if (!resp.ok) throw new Error(`cannot load the engine (${resp.status} ${resp.statusText})`)
    if (typeof WebAssembly.instantiateStreaming === 'function' && resp.headers.get('content-type')?.includes('application/wasm')) {
      instance = (await WebAssembly.instantiateStreaming(resp, go.importObject)).instance
    } else {
      instance = (await WebAssembly.instantiate(await resp.arrayBuffer(), go.importObject)).instance
    }
  } else {
    instance = (await WebAssembly.instantiate(opts.wasm, go.importObject)).instance
  }
  const ready = new Promise<void>((resolve) => {
    g.asmgraphEngineReady = resolve
  })
  void go.run(instance).then(() => opts.onExit?.())
  await ready
  if (!g.asmgraphEngine) throw new Error('the engine did not start')
  return g.asmgraphEngine
}
