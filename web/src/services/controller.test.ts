import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RpcClient, RpcError } from '../api/rpc'
import { Controller } from './controller'
import { useSim } from '../state/sim'
import { useUi } from '../state/ui'
import { useWorkspace } from '../state/workspace'
import { usePrefs } from '../state/prefs'
import { FakeServer, RpcFailure, catalog, fakeFetch, snapshot, tick, until } from '../test/fakeBackend'
import type { Puzzle } from '../api/types'

const initialWs = useWorkspace.getState()
const initialSim = useSim.getState()
const initialUi = useUi.getState()

let server: FakeServer
let c: Controller

async function boot(opts: { solutions?: Record<string, string>; handlers?: ConstructorParameters<typeof FakeServer>[0]; fetch?: Parameters<typeof fakeFetch>[0] } = {}) {
  server = new FakeServer(opts.handlers)
  const f = fakeFetch({ solutions: opts.solutions, ...opts.fetch })
  vi.stubGlobal('fetch', f.fn)
  c = new Controller({ wsUrl: 'ws://test/api/ws', WebSocketImpl: server.makeSocketClass(), schedule: (fn) => queueMicrotask(fn) })
  await c.init()
  await until(() => useWorkspace.getState().catalog !== null && useUi.getState().browser)
  return f
}

async function open() {
  expect(await c.openPuzzle('double-input')).toBe(true)
}

beforeEach(() => {
  localStorage.clear()
  useWorkspace.setState(initialWs, true)
  useSim.setState(initialSim, true)
  useUi.setState(initialUi, true)
  usePrefs.getState().set({ lastPuzzle: null })
})
afterEach(() => {
  c?.dispose()
  vi.unstubAllGlobals()
})

describe('RpcClient', () => {
  it('correlates responses, forwards notifications and maps errors', async () => {
    const s = new FakeServer({ boom: () => { throw new RpcFailure(-32002, 'compile failed', { diagnostics: [{ severity: 'error', message: 'x', cell: 'c' }] }) } })
    const events: unknown[] = []
    const rpc = new RpcClient({ url: 'ws://x', WebSocketImpl: s.makeSocketClass(), onEvent: (e) => events.push(e), reconnect: false })
    await rpc.connect()
    const [a, b] = await Promise.all([rpc.call<{ protocolVersion: string }>('initialize'), rpc.call('listCellTypes')])
    expect(a.protocolVersion).toBe('2')
    expect(b).toBeTruthy()
    const err = (await rpc.call('boom').catch((e: unknown) => e)) as RpcError
    expect(err).toBeInstanceOf(RpcError)
    expect(err.code).toBe(-32002)
    expect(err.diagnostics[0].cell).toBe('c')
    s.emit('tick.completed', { status: 'running' }, 1)
    expect(events).toHaveLength(1)
    rpc.close()
  })

  it('rejects pending calls when the connection drops, then reconnects', async () => {
    const s = new FakeServer({ slow: () => undefined })
    const states: string[] = []
    const rpc = new RpcClient({ url: 'ws://x', WebSocketImpl: s.makeSocketClass(), onState: (st) => states.push(st) })
    await rpc.connect()
    // Intercept the response so the call stays pending.
    s.sockets[0].deliver = () => {}
    const pending = rpc.call('slow')
    s.dropAll()
    await expect(pending).rejects.toMatchObject({ code: -1 })
    expect(states).toContain('reconnecting')
    await expect(rpc.call('initialize')).rejects.toMatchObject({ code: -1 })
    s.down = false
    await rpc.reconnectNow()
    expect(rpc.connectionState).toBe('open')
    rpc.close()
  })
})

describe('Controller', () => {
  it('connects, checks the protocol and lists puzzles', async () => {
    await boot()
    expect(server.methods().slice(0, 2)).toEqual(['initialize', 'listCellTypes'])
    expect(useSim.getState().conn).toBe('open')
    expect(useWorkspace.getState().puzzles[0].id).toBe('double-input')
  })

  it('reports a protocol mismatch', async () => {
    server = new FakeServer({ initialize: () => ({ protocolVersion: '99', version: 'x' }) })
    vi.stubGlobal('fetch', fakeFetch().fn)
    c = new Controller({ wsUrl: 'ws://t', WebSocketImpl: server.makeSocketClass(), schedule: queueMicrotask })
    await c.init()
    await until(() => !!useSim.getState().connError)
    expect(useSim.getState().connError).toMatch(/Protocol mismatch/)
  })

  it('loads a puzzle into the backend with an empty, auto-laid-out solution', async () => {
    await boot()
    await open()
    const ws = useWorkspace.getState()
    expect(server.calls.find((x) => x.method === 'loadPuzzle')?.params).toEqual({ yaml: 'puzzle-yaml' })
    expect(ws.solution?.programs).toEqual({ processor: '' })
    expect(Object.keys(ws.solution!.layout).sort()).toEqual(['input', 'output', 'processor'])
    expect(ws.solution!.layout.input.x).toBeLessThan(ws.solution!.layout.output.x)
    expect(ws.selectedCell).toBe('processor')
    await until(() => !!useWorkspace.getState().assemble.processor)
  })

  it('loads the most recent saved solution', async () => {
    await boot({ solutions: { main: 'schema_version: 1\npuzzle: double-input\nprograms:\n  processor: |\n    MOV P0, P1\n' } })
    await open()
    expect(useWorkspace.getState().solutionName).toBe('main')
    expect(useWorkspace.getState().solution?.programs.processor).toBe('MOV P0, P1\n')
    expect(useWorkspace.getState().dirty).toBe(false)
  })

  it('shows compile diagnostics without discarding the source', async () => {
    await boot({
      handlers: {
        compile: () => {
          throw new RpcFailure(-32002, 'solution does not compile', {
            diagnostics: [{ severity: 'error', message: 'unknown instruction MUL', cell: 'processor', sourceLine: 2, sourceColumn: 1 }],
          })
        },
      },
    })
    await open()
    c.setProgram('processor', 'MOV P0, ACC\nMUL 2\n')
    expect(await c.compile()).toBe(false)
    const ws = useWorkspace.getState()
    expect(ws.compile.state).toBe('error')
    expect(ws.diagnostics[0].message).toMatch(/MUL/)
    expect(ws.solution?.programs.processor).toBe('MOV P0, ACC\nMUL 2\n')
    expect(useUi.getState().bottomTab).toBe('diagnostics')
    // The backend received the edited solution before compiling.
    const loaded = server.calls.filter((x) => x.method === 'loadSolution').at(-1)?.params as { yaml: string }
    expect(loaded.yaml).toContain('MUL 2')
  })

  it('steps: loads, compiles, starts once, then follows the backend state', async () => {
    await boot()
    await open()
    c.connect({ cell: 'input', port: 'P0' }, { cell: 'processor', port: 'P0' })
    await Promise.all([c.step(), c.step(), c.step()]) // rapid key presses
    expect(server.methods().filter((m) => m === 'start')).toHaveLength(1)
    expect(server.methods().filter((m) => m === 'step')).toHaveLength(3)
    const order = server.methods().filter((m) => ['loadSolution', 'compile', 'start', 'step'].includes(m))
    expect(order.slice(0, 4)).toEqual(['loadSolution', 'compile', 'start', 'step'])
    expect(useSim.getState().view.tick).toBe(3)
    expect(useSim.getState().started).toBe(true)
  })

  it('applies incremental events in batches and resyncs after a gap', async () => {
    await boot()
    await open()
    await c.start(0)
    const getStates = () => server.methods().filter((m) => m === 'getState').length
    const before = getStates()
    server.emit('port.transfer', { from: { cell: 'input', port: 'P0' }, to: { cell: 'processor', port: 'P0' }, value: 42 }, 1)
    server.emit('cell.state', { ...snapshot(1).cells[0], state: { ACC: 42, BAK: 0 } }, 1, 'processor')
    server.emit('tick.completed', { status: 'running' }, 1)
    await until(() => useSim.getState().view.tick === 1)
    const sim = useSim.getState()
    expect(sim.view.cells.processor.state.ACC).toBe(42)
    expect(Object.values(sim.edges)[0]?.value).toBe(42)
    expect(sim.log.some((l) => l.text.includes('42'))).toBe(true)
    expect(getStates()).toBe(before)
    server.seq += 5 // events lost in transit
    server.emit('tick.completed', { status: 'running' }, 7)
    await until(() => getStates() > before)
  })

  it('invalidates the running simulation when the solution is edited', async () => {
    await boot()
    await open()
    await c.start(0)
    expect(useSim.getState().started).toBe(true)
    c.setProgram('processor', 'NOP')
    expect(useSim.getState().started).toBe(false)
    await c.step()
    expect(server.methods().filter((m) => m === 'loadSolution')).toHaveLength(2)
  })

  it('runs at maximum speed with lifecycle events and sampled snapshots', async () => {
    await boot()
    await open()
    usePrefs.getState().set({ speed: 'max' })
    await c.run()
    expect(server.calls.find((x) => x.method === 'setEventLevel')?.params).toEqual({ level: 'lifecycle' })
    expect(server.calls.find((x) => x.method === 'run')?.params).toEqual({ delayMs: 0 })
    expect(useSim.getState().running).toBe(true)
    await until(() => server.methods().filter((m) => m === 'getState').length >= 2, 1000)
    server.emit('simulation.completed', { test: 'positive', status: 'passed', passed: true, metrics: snapshot(9).metrics, outputs: [] }, 9)
    await until(() => !useSim.getState().running)
    expect(useSim.getState().view.status).toBe('passed')
    expect(useSim.getState().results.positive.passed).toBe(true)
  })

  it('saves solutions and clears the dirty flag', async () => {
    const f = await boot()
    await open()
    c.setProgram('processor', 'MOV P0, P1')
    expect(useWorkspace.getState().dirty).toBe(true)
    expect(await c.save('main')).toBe(true)
    expect(useWorkspace.getState().dirty).toBe(false)
    expect(f.puts[0].url).toBe('/api/puzzles/double-input/solutions/main')
    expect(JSON.parse(f.puts[0].body).yaml).toContain('MOV P0, P1')
  })

  it('recovers after the backend disconnects, keeping edits', async () => {
    await boot()
    await open()
    c.setProgram('processor', 'MOV P0, P1')
    await c.start(0)
    server.dropAll()
    await until(() => useSim.getState().conn === 'reconnecting')
    expect(useSim.getState().running).toBe(false)
    server.down = false
    const loadsBefore = server.methods().filter((m) => m === 'loadPuzzle').length
    c.reconnect()
    await until(() => server.methods().filter((m) => m === 'loadPuzzle').length > loadsBefore)
    await until(() => useSim.getState().notices.some((n) => /Reconnected/.test(n.text)))
    expect(useSim.getState().started).toBe(false)
    expect(useSim.getState().loadedVersion).toBe(-1)
    expect(useWorkspace.getState().solution?.programs.processor).toBe('MOV P0, P1')
    await c.step() // reloads the solution into the new session
    expect(server.methods().filter((m) => m === 'loadSolution').length).toBeGreaterThanOrEqual(2)
    await tick()
  })

  it('imports solutions only for the open puzzle', async () => {
    await boot()
    await open()
    expect(() => c.importYaml('schema_version: 1\npuzzle: other\n')).toThrow(/for puzzle "other"/)
    c.importYaml('schema_version: 1\npuzzle: double-input\nprograms:\n  processor: NOP\n')
    expect(useWorkspace.getState().solution?.programs.processor).toBe('NOP')
    expect(useWorkspace.getState().dirty).toBe(true)
    expect(c.exportYaml()?.yaml).toContain('NOP')
  })

  it('designs hardware: adds, configures and removes nodes, validated by the backend', async () => {
    const hw: Puzzle = {
      schema_version: 2,
      id: 'hw',
      cells: [
        { id: 'in', type: 'input', config: { stream: 'xs' } },
        { id: 'out', type: 'output', config: { stream: 'ys' } },
      ],
      hardware: { max_cost: 30 },
      tests: [{ name: 't', inputs: { xs: [1] }, outputs: { ys: [1] } }],
    }
    const spec = (t: string) => catalog.types.find((x) => x.type === t)!.hardware
    const effective = ['standard', 'stack'].map((t) => ({ type: t, spec: spec(t), ports: spec(t).ports, programMemory: spec(t).programMemory, cost: spec(t).cost, min: 0, max: 0, functions: spec(t).portFunctions?.functions.map((f) => f.name) }))
    let lastYaml = ''
    const f = await boot({
      fetch: { puzzle: hw, score: { passed: true, score: { cost: 26, ticks: 9, bytes: 6 }, improved: ['cost', 'ticks', 'bytes'], record: { puzzle: 'hw', bests: { cost: { score: { cost: 26, ticks: 9, bytes: 6 }, file: 'best-cost.yaml', achieved: '' } } } } },
      handlers: {
        loadPuzzle: () => ({ puzzle: hw, hardware: effective, diagnostics: [] }),
        loadSolution: (p) => {
          lastYaml = (p as { yaml: string }).yaml
          const over = lastYaml.includes('port_count: 8')
          const report = { allowed: true, nodes: [], totalCost: over ? 34 : 26, maxCost: 30, nodeCount: 1, counts: {} }
          if (over) throw new RpcFailure(-32001, 'solution is invalid', { diagnostics: [{ severity: 'error', code: 'solution.hardware.cost', path: 'hardware', message: 'hardware cost 34 exceeds this puzzle\'s budget of 30' }], hardware: report })
          return { diagnostics: [], hardware: report }
        },
        runTests: () => ({ puzzle: 'hw', passed: true, tests: [], score: { cost: 26, ticks: 9, bytes: 6 }, summary: { passedTests: 1, totalTests: 1, totalTicks: 9, maxTicks: 9, totalInstructions: 0, totalTransfers: 0, programBytes: {}, totalProgramBytes: 6, cellsSupplied: 3, cellsUsed: 3, connections: 2, hardwareCost: 26 } }),
      },
    })
    expect(await c.openPuzzle('hw')).toBe(true)
    const ws = () => useWorkspace.getState()
    expect(ws().hardwareCatalog?.map((e) => e.type)).toEqual(['standard', 'stack'])

    c.addNode('standard')
    expect(ws().solution?.hardware).toEqual([{ id: 'cpu1', type: 'standard' }])
    expect(ws().cells.find((x) => x.id === 'cpu1')).toMatchObject({ player: true, portCount: 4, programMemory: 32 })
    expect(ws().solution?.programs.cpu1).toBe('')
    // The backend checks the design after edits (debounced).
    await until(() => ws().hardwareReport?.totalCost === 26)
    expect(lastYaml).toContain('hardware:')

    // Shrinking the port count removes connections to ports that are gone.
    expect(c.connect({ cell: 'in', port: 'P0' }, { cell: 'cpu1', port: 'P3' }).ok).toBe(true)
    c.updateNode('cpu1', { port_count: 2 })
    expect(ws().solution?.connections).toEqual([])
    expect(useSim.getState().notices.some((n) => /Removed 1 connection/.test(n.text))).toBe(true)

    // Constraint violations come back from the backend with the report.
    c.updateNode('cpu1', { port_count: 8 })
    await until(() => ws().hardwareIssues.length === 1)
    expect(ws().hardwareIssues[0].message).toMatch(/budget of 30/)
    expect(ws().hardwareReport?.totalCost).toBe(34)

    // Passing all tests submits the solution for scoring.
    c.updateNode('cpu1', { port_count: 2 })
    await until(() => ws().hardwareIssues.length === 0)
    await c.runTests()
    await until(() => ws().lastScore !== null)
    expect(f.scorePosts[0]).toContain('id: cpu1')
    expect(ws().lastScore?.improved).toEqual(['cost', 'ticks', 'bytes'])

    c.removeNode('cpu1')
    expect(ws().solution?.hardware).toEqual([])
    expect(ws().solution?.programs.cpu1).toBeUndefined()
    expect(ws().cells.map((x) => x.id)).toEqual(['in', 'out'])
  })
})
