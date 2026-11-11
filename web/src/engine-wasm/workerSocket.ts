// A WebSocket look-alike backed by the engine Web Worker. The RPC client
// (api/rpc.ts) was written for a WebSocket to `asmgraph web`; with this
// class it talks to the in-browser engine without any changes, including
// its reconnect logic: if the engine fails, the "connection" closes and the
// client starts a fresh engine.

type WorkerMsg = { type: 'ready' } | { type: 'message'; data: string } | { type: 'fatal'; error: string }

export type WorkerFactory = () => Worker

const defaultFactory: WorkerFactory = () => new Worker(new URL('./engine.worker.ts', import.meta.url), { type: 'module', name: 'asmgraph-engine' })

/** The reason the last engine failed to start, for the connection banner. */
export let lastEngineError: string | null = null

export function workerSocketClass(factory: WorkerFactory = defaultFactory): typeof WebSocket {
  class WorkerSocket {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSING = 2
    static readonly CLOSED = 3
    readyState = 0
    onopen: ((ev: Event) => void) | null = null
    onmessage: ((ev: MessageEvent) => void) | null = null
    onerror: ((ev: Event) => void) | null = null
    onclose: ((ev: CloseEvent) => void) | null = null
    private worker: Worker | null

    constructor(_url?: string) {
      this.worker = factory()
      this.worker.postMessage({ type: 'init', base: new URL('engine/', document.baseURI).href })
      this.worker.onmessage = (e: MessageEvent<WorkerMsg>) => {
        const m = e.data
        if (m.type === 'ready') {
          lastEngineError = null
          this.readyState = 1
          this.onopen?.(new Event('open'))
        } else if (m.type === 'message') {
          this.onmessage?.(new MessageEvent('message', { data: m.data }))
        } else {
          lastEngineError = m.error
          this.shutdown()
        }
      }
      this.worker.onerror = (e) => {
        lastEngineError = e.message || 'the engine failed to load'
        this.shutdown()
      }
    }

    send(data: string) {
      if (this.readyState === 1) this.worker?.postMessage(data)
    }

    close() {
      this.shutdown()
    }

    private shutdown() {
      if (this.readyState === 3) return
      this.readyState = 3
      this.worker?.terminate()
      this.worker = null
      queueMicrotask(() => {
        this.onerror?.(new Event('error'))
        this.onclose?.(new CloseEvent('close'))
      })
    }
  }
  return WorkerSocket as unknown as typeof WebSocket
}
