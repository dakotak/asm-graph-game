// JSON-RPC 2.0 client over WebSocket. It speaks exactly the protocol of
// `asmgraph serve --stdio` (see docs/protocol.md); the server gives every
// WebSocket connection its own simulation session.
import type { Diagnostic, SimEvent } from './types'

export type ConnectionState = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'

export class RpcError extends Error {
  readonly code: number
  readonly data: unknown
  constructor(code: number, message: string, data?: unknown) {
    super(message)
    this.name = 'RpcError'
    this.code = code
    this.data = data
  }
  /** Diagnostics attached to validation (-32001) and compile (-32002) errors. */
  get diagnostics(): Diagnostic[] {
    const d = (this.data as { diagnostics?: Diagnostic[] } | undefined)?.diagnostics
    return Array.isArray(d) ? d : []
  }
}

export const RPC_CONNECTION_LOST = -1
export const RPC_TIMEOUT = -2

export interface RpcClientOptions {
  url: string
  onEvent?: (e: SimEvent) => void
  onState?: (s: ConnectionState, info: { reconnected: boolean; error?: string }) => void
  requestTimeoutMs?: number
  /** Reconnect automatically after an unexpected close. */
  reconnect?: boolean
  WebSocketImpl?: typeof WebSocket
}

interface Pending {
  resolve: (v: unknown) => void
  reject: (e: unknown) => void
  timer: ReturnType<typeof setTimeout>
}

export class RpcClient {
  private ws: WebSocket | null = null
  private nextId = 1
  private pending = new Map<number, Pending>()
  private state: ConnectionState = 'idle'
  private closedByUser = false
  private attempts = 0
  private everOpened = false
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private readonly opts: Required<Omit<RpcClientOptions, 'onEvent' | 'onState' | 'WebSocketImpl'>> &
    Pick<RpcClientOptions, 'onEvent' | 'onState' | 'WebSocketImpl'>

  constructor(opts: RpcClientOptions) {
    this.opts = { requestTimeoutMs: 30_000, reconnect: true, ...opts }
  }

  get connectionState(): ConnectionState {
    return this.state
  }

  private setState(s: ConnectionState, error?: string) {
    this.state = s
    this.opts.onState?.(s, { reconnected: s === 'open' && this.everOpened && this.attempts > 0, error })
  }

  /** Opens the socket. Resolves once connected; rejects if the first attempt fails. */
  connect(): Promise<void> {
    this.closedByUser = false
    if (this.retryTimer) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    return new Promise((resolve, reject) => {
      const Impl = this.opts.WebSocketImpl ?? WebSocket
      let ws: WebSocket
      try {
        ws = new Impl(this.opts.url)
      } catch (e) {
        this.setState('closed', String(e))
        reject(e)
        return
      }
      this.ws = ws
      this.setState(this.attempts > 0 ? 'reconnecting' : 'connecting')
      let opened = false
      ws.onopen = () => {
        opened = true
        const reconnected = this.everOpened
        this.everOpened = true
        this.state = 'open'
        this.opts.onState?.('open', { reconnected })
        this.attempts = 0
        resolve()
      }
      ws.onmessage = (ev) => this.onMessage(ev.data)
      ws.onerror = () => {
        /* onclose follows and carries the handling */
      }
      ws.onclose = () => {
        if (this.ws !== ws) return
        this.ws = null
        this.failPending('connection lost')
        if (!opened) reject(new RpcError(RPC_CONNECTION_LOST, 'cannot start the asmgraph engine'))
        if (this.closedByUser) {
          this.setState('closed')
          return
        }
        if (this.opts.reconnect) {
          this.scheduleReconnect()
        } else {
          this.setState('closed', 'connection lost')
        }
      }
    })
  }

  private scheduleReconnect() {
    this.attempts++
    const delay = Math.min(10_000, 250 * 2 ** Math.min(this.attempts, 6))
    this.setState('reconnecting', `connection lost; retrying in ${Math.round(delay / 100) / 10}s`)
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      this.connect().catch(() => {
        /* onclose schedules the next attempt */
      })
    }, delay)
  }

  /** Reconnect now (e.g. from a "Reconnect" button). */
  reconnectNow(): Promise<void> {
    if (this.state === 'open') return Promise.resolve()
    if (this.ws) {
      this.ws.onclose = null
      this.ws.close()
      this.ws = null
    }
    this.attempts = Math.max(this.attempts, 1)
    return this.connect()
  }

  close() {
    this.closedByUser = true
    if (this.retryTimer) clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.ws?.close()
    this.failPending('client closed')
    this.setState('closed')
  }

  private failPending(reason: string) {
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer)
      p.reject(new RpcError(RPC_CONNECTION_LOST, reason))
      this.pending.delete(id)
    }
  }

  private onMessage(raw: unknown) {
    if (typeof raw !== 'string') return
    let msg: unknown
    try {
      msg = JSON.parse(raw)
    } catch {
      return
    }
    const messages = Array.isArray(msg) ? msg : [msg]
    for (const m of messages) {
      if (!m || typeof m !== 'object') continue
      const o = m as { id?: unknown; method?: string; params?: unknown; result?: unknown; error?: { code: number; message: string; data?: unknown } }
      if (o.method === 'event' && o.id === undefined) {
        this.opts.onEvent?.(o.params as SimEvent)
        continue
      }
      if (typeof o.id !== 'number') continue
      const p = this.pending.get(o.id)
      if (!p) continue
      this.pending.delete(o.id)
      clearTimeout(p.timer)
      if (o.error) p.reject(new RpcError(o.error.code, o.error.message, o.error.data))
      else p.resolve(o.result)
    }
  }

  call<T = unknown>(method: string, params?: unknown): Promise<T> {
    const ws = this.ws
    if (!ws || this.state !== 'open') {
      return Promise.reject(new RpcError(RPC_CONNECTION_LOST, 'the asmgraph engine is not running'))
    }
    const id = this.nextId++
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new RpcError(RPC_TIMEOUT, `${method} timed out`))
      }, this.opts.requestTimeoutMs)
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject, timer })
      const msg: Record<string, unknown> = { jsonrpc: '2.0', id, method }
      if (params !== undefined) msg.params = params
      ws.send(JSON.stringify(msg))
    })
  }
}

/** Default WebSocket URL for the page's origin. */
export function defaultWsUrl(loc: Location = window.location): string {
  const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${loc.host}/api/ws`
}
