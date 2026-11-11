// In-memory stand-ins used by unit tests: a fake engine connection (a
// WebSocket look-alike speaking JSON-RPC, like the engine worker) and a
// fake puzzle library. End-to-end tests (e2e/) run the real engine, compiled
// to WebAssembly, in a real browser instead.
import { ApiError, type Library } from '../api/library'
import type { CellTypeCatalog, Puzzle, PuzzleSummary, ScoreResponse, Snapshot } from '../api/types'

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
      hardware: {
        category: 'processor',
        ports: { min: 1, max: 8, default: 4 },
        programMemory: { min: 1, max: 1024, default: 32 },
        cost: { base: 10, perPort: 2, properties: [{ property: 'program_memory', cost: 1, per: 8 }] },
        overrides: { cost: true, defaults: true },
      },
    },
    {
      type: 'stack',
      description: 'passive stack',
      programmable: false,
      config: [{ name: 'capacity', type: 'int', default: 8, min: 1, max: 64 }],
      hardware: {
        category: 'peripheral',
        ports: { min: 2, max: 8, default: 4 },
        portFunctions: {
          functions: [
            { name: 'PUSH', direction: 'in', summary: 'push', min: 1 },
            { name: 'POP', direction: 'out', summary: 'pop', min: 1 },
            { name: 'PEEK', direction: 'out', summary: 'peek' },
            { name: 'SIZE', direction: 'out', summary: 'size' },
          ],
          configurable: true,
          default: ['PUSH', 'POP', 'PEEK', 'SIZE'],
        },
        cost: { base: 5, perPort: 1, properties: [{ property: 'capacity', cost: 1, per: 4 }] },
        overrides: { cost: true, defaults: true },
      },
    },
    { type: 'input', description: 'in', programmable: false, config: [], hardware: { category: 'io', ports: { min: 1, max: 8, default: 1 }, cost: { base: 0, perPort: 0 }, puzzleOnly: true, overrides: { cost: false, defaults: false } } },
    { type: 'output', description: 'out', programmable: false, config: [], hardware: { category: 'io', ports: { min: 1, max: 8, default: 1 }, cost: { base: 0, perPort: 0 }, puzzleOnly: true, overrides: { cost: false, defaults: false } } },
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
      { id: 'processor', type: 'standard', category: 'processor', programmable: true, pc: tick % 3, address: 0, stage: 'ready', blocked: false, ports: [], state: { ACC: tick, BAK: 0 }, instructionsCompleted: tick },
    ],
    connections: [],
    inputs: [{ cell: 'input', stream: 'numbers', position: 0, length: 2 }],
    outputs: [{ cell: 'output', stream: 'result', state: 'pending', expected: [2, 4], received: [] }],
    metrics: { ticks: tick, instructions: 0, transfers: 0, programBytes: {}, totalProgramBytes: 0, cellsSupplied: 3, cellsUsed: 0, connections: 0, hardwareCost: 0 },
  }
}

function defaultHandlers(): Record<string, Handler> {
  let tick = 0
  return {
    initialize: () => ({ protocolVersion: '2', version: 'test' }),
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

/** An in-memory puzzle library with one puzzle (double-input by default). */
export function fakeLibrary(opts: { solutions?: Record<string, string>; puzzle?: Puzzle; score?: unknown } = {}) {
  const solutions = { ...(opts.solutions ?? {}) }
  const puts: { puzzle: string; name: string; yaml: string }[] = []
  const scorePosts: string[] = []
  const pz = opts.puzzle ?? puzzle
  const id = pz.id
  const notFound = () => new ApiError('not found', 404)
  const library: Library = {
    listPuzzles: async () => [
      { id, name: pz.name ?? id, description: '', file: 'x', cellTypes: [], cells: 3, programmableCells: 1, tests: 2, valid: true, solutions: 0, hardware: !!pz.hardware },
    ],
    getPuzzle: async (pid) => {
      if (pid !== id) throw notFound()
      return { summary: { id, valid: true } as PuzzleSummary, yaml: 'puzzle-yaml', puzzle: pz }
    },
    listSolutions: async () => Object.keys(solutions).map((name) => ({ name, modified: '2026-01-01T00:00:00Z', size: 1 })),
    getSolution: async (_pid, name) => {
      if (!(name in solutions)) throw notFound()
      return { name, yaml: solutions[name] }
    },
    saveSolution: async (pid, name, yaml) => {
      puts.push({ puzzle: pid, name, yaml })
      solutions[name] = yaml
      return { name, saved: true }
    },
    deleteSolution: async (_pid, name) => {
      delete solutions[name]
    },
    getScores: async () => ({ puzzle: id, bests: {} }),
    submitScore: async (_pid, yaml) => {
      scorePosts.push(yaml)
      return (opts.score ?? { passed: true, improved: [], record: { puzzle: id, bests: {} } }) as ScoreResponse
    },
    getBestSolution: async () => {
      throw notFound()
    },
    importPuzzle: async () => {
      throw new ApiError('not supported', 400)
    },
    removePuzzle: async () => {},
  }
  return { library, puts, solutions, scorePosts }
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
