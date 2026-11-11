// An in-memory stand-in for `asmgraph web` used by unit tests: a fake
// WebSocket speaking JSON-RPC and a fetch() for the HTTP library API.
// End-to-end tests (e2e/) run against the real Go server instead.
import type { CellTypeCatalog, Puzzle, Snapshot } from '../api/types'

type Handler = (params: unknown) => unknown

export class RpcFailure {
  constructor(
    public code: number,
    public message: string,
    public data?: unknown,
  ) {}
}

export class FakeServer {
  down = false
  sockets: FakeSocket[] = []
  calls: { method: string; params: unknown }[] = []
  handlers: Record<string, Handler> = {}
  seq = 0

  constructor(defaults: Record<string, Handler> = {}) {
    this.handlers = { ...defaultHandlers(), ...defaults }
  }

  methods(): string[] {
    return this.calls.map((c) => c.method)
  }

  emit(type: string, data: unknown, tick = 0, cell?: string) {
    this.seq++
    const msg = JSON.stringify({ jsonrpc: '2.0', method: 'event', params: { seq: this.seq, tick, type, cell, data } })
    for (const s of this.sockets) if (s.readyState === 1) s.deliver(msg)
  }

  /** Simulates the server going away. */
  dropAll() {
    this.down = true
    for (const s of [...this.sockets]) s.serverClose()
  }

  makeSocketClass(): typeof WebSocket {
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    const server = this
    return class extends FakeSocket {
      constructor(url: string) {
        super(url, server)
      }
    } as unknown as typeof WebSocket
  }
}

export class FakeSocket {
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  constructor(
    public url: string,
    private server: FakeServer,
  ) {
    server.sockets.push(this)
    setTimeout(() => {
      if (server.down) {
        this.readyState = 3
        this.onclose?.()
      } else {
        this.readyState = 1
        this.onopen?.()
      }
    }, 0)
  }
  send(raw: string) {
    const req = JSON.parse(raw) as { id: number; method: string; params?: unknown }
    this.server.calls.push({ method: req.method, params: req.params })
    const h = this.server.handlers[req.method]
    let reply: unknown
    try {
      if (!h) throw new RpcFailure(-32601, `method ${req.method} not found`)
      reply = { jsonrpc: '2.0', id: req.id, result: h(req.params) ?? {} }
    } catch (e) {
      const f = e instanceof RpcFailure ? e : new RpcFailure(-32603, String(e))
      reply = { jsonrpc: '2.0', id: req.id, error: { code: f.code, message: f.message, data: f.data } }
    }
    setTimeout(() => this.deliver(JSON.stringify(reply)), 0)
  }
  deliver(data: string) {
    if (this.readyState === 1) this.onmessage?.({ data })
  }
  close() {
    this.readyState = 3
    this.server.sockets = this.server.sockets.filter((s) => s !== this)
    setTimeout(() => this.onclose?.(), 0)
  }
  serverClose() {
    this.readyState = 3
    this.server.sockets = this.server.sockets.filter((s) => s !== this)
    this.onclose?.()
  }
}

export const catalog: CellTypeCatalog = {
  types: [
    {
      type: 'standard',
      description: 'std',
      programmable: true,
      instructions: [{ mnemonic: 'MOV', opcode: '0x01', operands: ['source', 'destination'], summary: 'move', core: true }],
      registers: [{ code: 0, name: 'NIL', summary: '' }, { code: 1, name: 'ACC', summary: '' }],
    },
    { type: 'input', description: 'in', programmable: false, config: [] },
    { type: 'output', description: 'out', programmable: false, config: [] },
  ],
  operandKinds: { source: 'src', destination: 'dst', target: 'tgt' },
  ports: [{ name: 'P0', physical: true, description: '' }],
  syntax: { comments: ['#', ';'], labelSuffix: ':', caseSensitive: false, operandSeparator: ',', integerFormats: [] },
}

export const puzzle: Puzzle = {
  schema_version: 1,
  id: 'double-input',
  name: 'Double the Input',
  cells: [
    { id: 'input', type: 'input', ports: ['P0'], config: { stream: 'numbers' } },
    { id: 'processor', type: 'standard', program_memory: 32 },
    { id: 'output', type: 'output', ports: ['P0'], config: { stream: 'result' } },
  ],
  tests: [
    { name: 'positive', inputs: { numbers: [1, 2] }, outputs: { result: [2, 4] } },
    { name: 'negative', inputs: { numbers: [-1] }, outputs: { result: [-2] } },
  ],
}

export function snapshot(tick = 0, status: Snapshot['status'] = 'ready'): Snapshot {
  return {
    puzzle: 'double-input',
    test: 'positive',
    testIndex: 0,
    tick,
    status,
    cells: [
      { id: 'processor', type: 'standard', programmable: true, pc: tick % 3, address: 0, stage: 'ready', blocked: false, ports: [], state: { ACC: tick, BAK: 0 }, instructionsCompleted: tick },
    ],
    connections: [],
    inputs: [{ cell: 'input', stream: 'numbers', position: 0, length: 2 }],
    outputs: [{ cell: 'output', stream: 'result', state: 'pending', expected: [2, 4], received: [] }],
    metrics: { ticks: tick, instructions: 0, transfers: 0, programBytes: {}, totalProgramBytes: 0, cellsSupplied: 3, cellsUsed: 0, connections: 0 },
  }
}

function defaultHandlers(): Record<string, Handler> {
  let tick = 0
  return {
    initialize: () => ({ protocolVersion: '1', version: 'test' }),
    listCellTypes: () => catalog,
    loadPuzzle: () => ({ puzzle, diagnostics: [] }),
    loadSolution: () => ({ diagnostics: [] }),
    setBreakpoints: (p) => p,
    setEventLevel: (p) => p,
    assemble: (p) => ({ ok: true, bytes: ((p as { source: string }).source.trim() ? 10 : 0), limit: 32, diagnostics: [], disassembly: [] }),
    compile: () => ({ programs: [{ cell: 'processor', type: 'standard', bytes: 10, limit: 32, instructions: 4, hex: '', disassembly: [] }], totalBytes: 10, diagnostics: [] }),
    start: () => {
      tick = 0
      return snapshot(0)
    },
    step: () => {
      tick++
      return { status: 'running', tick }
    },
    getState: () => snapshot(tick, tick > 0 ? 'running' : 'ready'),
    run: () => ({ running: true }),
    pause: () => ({ status: 'running', tick }),
    reset: () => {
      tick = 0
      return snapshot(0)
    },
    runTests: () => ({
      puzzle: 'double-input',
      passed: true,
      tests: [],
      summary: { passedTests: 2, totalTests: 2, totalTicks: 0, maxTicks: 0, totalInstructions: 0, totalTransfers: 0, programBytes: {}, totalProgramBytes: 10, cellsSupplied: 3, cellsUsed: 3, connections: 2 },
    }),
  }
}

/** A fetch() implementing the HTTP library endpoints. */
export function fakeFetch(opts: { solutions?: Record<string, string> } = {}) {
  const solutions = { ...(opts.solutions ?? {}) }
  const puts: { url: string; body: string }[] = []
  const fn = async (url: string, init?: RequestInit) => {
    const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })
    if (url === '/api/puzzles') {
      return json(200, { puzzles: [{ id: 'double-input', name: 'Double the Input', description: '', file: 'x', cellTypes: [], cells: 3, programmableCells: 1, tests: 2, valid: true, solutions: 0 }] })
    }
    if (url === '/api/puzzles/double-input') {
      return json(200, { summary: { id: 'double-input', valid: true }, yaml: 'puzzle-yaml', puzzle })
    }
    if (url === '/api/puzzles/double-input/solutions') {
      return json(200, { solutions: Object.keys(solutions).map((name) => ({ name, modified: '2026-01-01T00:00:00Z', size: 1 })) })
    }
    const m = /^\/api\/puzzles\/double-input\/solutions\/(.+)$/.exec(url)
    if (m) {
      if (init?.method === 'PUT') {
        puts.push({ url, body: String(init.body) })
        solutions[decodeURIComponent(m[1])] = JSON.parse(String(init.body)).yaml
        return json(200, { name: m[1], saved: true })
      }
      const y = solutions[decodeURIComponent(m[1])]
      return y ? json(200, { name: m[1], yaml: y }) : json(404, { error: 'no such solution' })
    }
    return json(404, { error: 'not found' })
  }
  return { fn, puts, solutions }
}

export const tick = (ms = 0) => new Promise((r) => setTimeout(r, ms))

/** Waits until cond() is true (polling), failing after a timeout. */
export async function until(cond: () => boolean, ms = 2000) {
  const t0 = Date.now()
  while (!cond()) {
    if (Date.now() - t0 > ms) throw new Error('condition not met in time')
    await tick(5)
  }
}
