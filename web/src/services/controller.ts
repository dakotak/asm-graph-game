// The controller is the only code that talks to the backend. It keeps the
// backend session in sync with the edited solution, drives execution, and
// turns committed events into store updates. It never simulates anything:
// all machine behaviour comes from the Go engine.
import { ApiError, http } from '../api/http'
import { RpcClient, RpcError, defaultWsUrl, type ConnectionState } from '../api/rpc'
import {
  PROTOCOL_VERSION,
  type AssembleResult,
  type CellTypeCatalog,
  type CompileResult,
  type Diagnostic,
  type EffectiveHardware,
  type HardwareReport,
  type EventLevel,
  type PausedData,
  type SimEvent,
  type Snapshot,
  type SuiteResult,
  type TestResult,
} from '../api/types'
import { connectionId, type ConnectCheck } from '../lib/connections'
import { formatEvent, RingBuffer, type LogEntry } from '../lib/eventLog'
import { nextNodeId, resolveCells, type CellView } from '../lib/hardware'
import { autoLayout, NODE_HEIGHT, NODE_WIDTH } from '../lib/layout'
import { adaptToPuzzle, emptySolution, parseSolution, serializeSolution, type Position, type SolutionModel } from '../lib/solution'
import { applyEvents, emptyView, viewFromSnapshot } from '../lib/simReducer'
import { isTerminal } from '../lib/status'
import { clearDraft, loadDraft, markSolved, saveDraft, SPEEDS, usePrefs } from '../state/prefs'
import { notify, useSim, type EdgeActivity } from '../state/sim'
import { useUi } from '../state/ui'
import { cellStream, findCell, isProgrammableType, useWorkspace } from '../state/workspace'
import type { Endpoint, Puzzle } from '../api/types'

const MAX_QUEUED_EVENTS = 5000
const LOG_CAPACITY = 1000
const POLL_MS = 150
const ASSEMBLE_DEBOUNCE_MS = 250
const DRAFT_DEBOUNCE_MS = 1000
const HARDWARE_CHECK_MS = 300

export interface ControllerOptions {
  wsUrl?: string
  WebSocketImpl?: typeof WebSocket
  /** frame scheduler; defaults to requestAnimationFrame */
  schedule?: (fn: () => void) => void
}

export class Controller {
  rpc: RpcClient | null = null
  private initialized = false
  private queue: SimEvent[] = []
  private flushScheduled = false
  private ring = new RingBuffer<LogEntry>(LOG_CAPACITY)
  private lastSeq = 0
  private overflowed = false
  private resyncing = false
  /** bumps whenever the machine is replaced (start, reset, invalidate) */
  private epoch = 0
  private edgeSerial = 0
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private level: EventLevel | null = null
  private assembleTimers = new Map<string, ReturnType<typeof setTimeout>>()
  private draftTimer: ReturnType<typeof setTimeout> | null = null
  private hardwareTimer: ReturnType<typeof setTimeout> | null = null
  private schedule: (fn: () => void) => void
  /** Execution commands run one at a time, so rapid key presses never interleave. */
  private chain: Promise<unknown> = Promise.resolve()

  private serial<T>(fn: () => Promise<T>): Promise<T> {
    const p = this.chain.then(fn, fn)
    this.chain = p.catch(() => {})
    return p
  }

  constructor(private opts: ControllerOptions = {}) {
    this.schedule =
      opts.schedule ??
      ((fn) => {
        // rAF batches updates per frame; background tabs throttle rAF, so
        // fall back to a timer there to keep the state current.
        if (typeof requestAnimationFrame === 'function' && typeof document !== 'undefined' && !document.hidden) requestAnimationFrame(() => fn())
        else setTimeout(fn, 50)
      })
  }

  // --- connection ----------------------------------------------------------

  async init(): Promise<void> {
    this.rpc = new RpcClient({
      url: this.opts.wsUrl ?? defaultWsUrl(),
      WebSocketImpl: this.opts.WebSocketImpl,
      onEvent: (e) => this.handleEvent(e),
      onState: (s, info) => this.onConnState(s, info.error),
    })
    try {
      await this.rpc.connect()
    } catch {
      // The client keeps retrying; the banner shows the state.
    }
  }

  dispose() {
    this.stopPolling()
    this.rpc?.close()
  }

  private onConnState(s: ConnectionState, error?: string) {
    useSim.setState({ conn: s, connError: s === 'open' ? null : (error ?? null) })
    if (s === 'open') void this.onOpen()
    if (s === 'reconnecting' || s === 'closed') {
      this.stopPolling()
      useSim.setState({ running: false })
    }
  }

  private async onOpen() {
    const first = !this.initialized
    this.lastSeq = 0 // every connection is a new session with its own seq
    this.level = null
    try {
      const info = await this.call<{ protocolVersion: string; version: string }>('initialize')
      if (info.protocolVersion !== PROTOCOL_VERSION) {
        useSim.setState({ connError: `Protocol mismatch: server speaks ${info.protocolVersion}, this app needs ${PROTOCOL_VERSION}.` })
        notify('error', `Protocol mismatch: server ${info.protocolVersion}, client ${PROTOCOL_VERSION}. Rebuild the frontend.`, 0)
        return
      }
      useSim.setState({ serverVersion: info.version })
      const catalog = await this.call<CellTypeCatalog>('listCellTypes')
      useWorkspace.getState().setCatalog(catalog)
      this.initialized = true
      if (first) {
        await this.refreshPuzzles()
        const last = usePrefs.getState().lastPuzzle
        const puzzles = useWorkspace.getState().puzzles
        if (last && puzzles.some((p) => p.id === last)) await this.openPuzzle(last)
        else useUi.setState({ browser: true })
      } else {
        // A new connection is a new backend session: reload the puzzle and
        // mark the solution as not loaded. Editor content is untouched.
        const doc = useWorkspace.getState().puzzleDoc
        if (doc) await this.call('loadPuzzle', { yaml: doc.yaml })
        this.resetSimState()
        notify('warn', 'Reconnected to the server. The simulation was reset; your edits are intact.')
        await this.pushBreakpoints()
      }
    } catch (e) {
      notify('error', `Server initialization failed: ${errorText(e)}`)
    }
  }

  reconnect() {
    this.rpc?.reconnectNow().catch(() => notify('error', 'Still cannot reach the asmgraph server.'))
  }

  private call<T>(method: string, params?: unknown): Promise<T> {
    if (!this.rpc) return Promise.reject(new RpcError(-1, 'not connected'))
    return this.rpc.call<T>(method, params)
  }

  // --- events --------------------------------------------------------------

  /** Queues an event; events are applied in batches once per frame. */
  handleEvent(e: SimEvent) {
    this.queue.push(e)
    if (this.queue.length > MAX_QUEUED_EVENTS) {
      // The UI cannot keep up: drop the backlog and resync from a snapshot.
      this.queue = []
      this.overflowed = true
    }
    if (!this.flushScheduled) {
      this.flushScheduled = true
      this.schedule(() => this.flush())
    }
  }

  /** Applies queued events to the stores. Exposed for tests. */
  flush() {
    this.flushScheduled = false
    const events = this.queue
    this.queue = []
    if (events.length === 0 && !this.overflowed) return
    const sim = useSim.getState()
    const r = applyEvents(sim.view, this.lastSeq, events)
    this.lastSeq = r.lastSeq
    for (const e of events) {
      const entry = formatEvent(e)
      if (entry) this.ring.push(entry)
    }
    let edges = sim.edges
    if (r.transfers.length > 0 && usePrefs.getState().animateTransfers) {
      edges = { ...edges }
      for (const t of r.transfers) {
        const act: EdgeActivity = { value: t.value, tick: t.tick, serial: ++this.edgeSerial, from: `${t.from.cell}:${t.from.port}` }
        edges[connectionId({ from: t.from, to: t.to })] = act
      }
    }
    const patch: Partial<ReturnType<typeof useSim.getState>> = { view: r.view, edges, log: this.ring.toArray() }
    const paused = [...events].reverse().find((e) => e.type === 'simulation.paused')
    if (paused) {
      patch.pause = paused.data as PausedData
      patch.running = false
    }
    if (r.terminal) {
      patch.running = false
      this.stopPolling()
      if (r.view.result) useSim.setState({ results: { ...sim.results, [r.view.result.test]: r.view.result } })
    }
    useSim.setState(patch)
    if (paused || r.terminal) this.stopPolling()
    if (r.resync || this.overflowed) {
      this.overflowed = false
      void this.resync()
    }
  }

  /** Replaces the local view with a full backend snapshot. */
  async resync() {
    if (this.resyncing || !useSim.getState().started) return
    this.resyncing = true
    const epoch = this.epoch
    try {
      const snap = await this.call<Snapshot>('getState')
      const prev = useSim.getState().view
      // Ignore responses that are older than what events already applied
      // (a poll can race a completion event) or that belong to a machine
      // that has since been replaced.
      if (epoch !== this.epoch || snap.tick < prev.tick || (isTerminal(prev.status) && !isTerminal(snap.status))) return
      const view = viewFromSnapshot(snap)
      if (isTerminal(snap.status) && prev.result) view.result = prev.result
      useSim.setState({ view })
    } catch (e) {
      if (!(e instanceof RpcError && e.code === -32003)) notify('warn', `Could not refresh state: ${errorText(e)}`)
    } finally {
      this.resyncing = false
    }
  }

  private async setLevel(level: EventLevel) {
    if (this.level === level) return
    await this.call('setEventLevel', { level })
    this.level = level
  }

  private startPolling() {
    this.stopPolling()
    this.pollTimer = setInterval(() => void this.resync(), POLL_MS)
  }

  private stopPolling() {
    if (this.pollTimer) clearInterval(this.pollTimer)
    this.pollTimer = null
  }

  private resetSimState() {
    this.epoch++
    this.stopPolling()
    this.ring.clear()
    this.queue = []
    this.lastSeq = 0
    useSim.setState({ loadedVersion: -1, started: false, running: false, view: emptyView, pause: null, edges: {}, log: [] })
  }

  // --- puzzle library --------------------------------------------------------

  async refreshPuzzles() {
    try {
      useWorkspace.getState().setPuzzles(await http.listPuzzles())
    } catch (e) {
      notify('error', `Cannot list puzzles: ${errorText(e)}`)
    }
  }

  private programmable = (type: string) => isProgrammableType(useWorkspace.getState().catalog, type)

  /** Opens a puzzle and its most recent solution (or a recovered draft). */
  async openPuzzle(id: string): Promise<boolean> {
    useSim.setState({ busy: 'Loading puzzle…' })
    try {
      const doc = await http.getPuzzle(id)
      if (!doc.summary.valid) {
        notify('error', `Puzzle "${id}" is invalid: ${doc.summary.diagnostics?.[0]?.message ?? 'see diagnostics'}`)
        useWorkspace.getState().setDiagnostics(doc.summary.diagnostics ?? [])
        return false
      }
      const loaded = await this.call<{ hardware?: EffectiveHardware[] | null }>('loadPuzzle', { yaml: doc.yaml })
      useWorkspace.getState().setHardwareCatalog(doc.puzzle.hardware ? (loaded?.hardware ?? []) : null)
      let model: SolutionModel | null = null
      let name = ''
      let dirty = false
      const draft = loadDraft(id)
      if (draft) {
        try {
          model = adaptToPuzzle(parseSolution(draft.yaml), doc.puzzle, this.programmable).solution
          name = draft.name
          dirty = true
          notify('info', 'Restored your unsaved draft for this puzzle.')
        } catch {
          clearDraft(id)
        }
      }
      if (!model) {
        const saved = await http.listSolutions(id).catch(() => [])
        if (saved.length > 0) {
          const latest = [...saved].sort((a, b) => b.modified.localeCompare(a.modified))[0]
          const s = await http.getSolution(id, latest.name)
          try {
            model = adaptToPuzzle(parseSolution(s.yaml), doc.puzzle, this.programmable).solution
            name = latest.name
          } catch (e) {
            notify('warn', `Saved solution "${latest.name}" could not be read: ${errorText(e)}`)
          }
        }
      }
      if (!model) model = emptySolution(doc.puzzle, this.programmable)
      model = this.withLayout(model, doc.puzzle)
      this.resetSimState()
      useSim.setState({ results: {}, testIndex: 0 })
      useWorkspace.getState().openPuzzle(doc, model, name, dirty)
      usePrefs.getState().set({ lastPuzzle: id })
      this.assembleAll()
      this.scheduleHardwareCheck(0)
      void this.loadBests(id)
      void this.pushBreakpoints()
      useUi.setState({ fitView: useUi.getState().fitView + 1 })
      return true
    } catch (e) {
      notify('error', `Cannot open puzzle "${id}": ${errorText(e)}`)
      return false
    } finally {
      useSim.setState({ busy: null })
    }
  }

  /** Fills in positions for cells that have none (auto layout when none have one). */
  private withLayout(model: SolutionModel, puzzle: Puzzle, force = false): SolutionModel {
    const ws = useWorkspace.getState()
    const cells: CellView[] = resolveCells(puzzle, model, ws.catalog, ws.hardwareCatalog, null)
    const missing = cells.filter((c) => !model.layout[c.id]).map((c) => c.id)
    if (!force && missing.length === 0) return model
    // A cell is an input/output if its stream is a test-case input/output
    // stream; this works for any cell type, including plugins.
    const t0 = puzzle.tests[0]
    const isIO = (dir: 'inputs' | 'outputs') => (c: CellView) => {
      const s = cellStream(c)
      return !!s && !!t0 && s in (t0[dir] ?? {})
    }
    const auto = autoLayout(cells, model.connections, isIO('inputs'), isIO('outputs'))
    const layout: Record<string, Position> = force || missing.length === cells.length ? auto : { ...model.layout }
    if (!force) for (const id of missing) layout[id] = auto[id]
    return { ...model, layout }
  }

  // --- editing -----------------------------------------------------------------

  setProgram(cell: string, source: string) {
    useWorkspace.getState().setProgram(cell, source)
    this.invalidate()
    this.scheduleAssemble(cell)
    this.scheduleDraft()
  }

  connect(a: Endpoint, b: Endpoint): ConnectCheck {
    const r = useWorkspace.getState().connect(a, b)
    if (r.ok) {
      this.invalidate()
      this.scheduleDraft()
    }
    return r
  }

  disconnect(id: string) {
    useWorkspace.getState().disconnect(id)
    this.invalidate()
    this.scheduleDraft()
  }

  movedCells(pos: Record<string, Position>) {
    useWorkspace.getState().setPositions(pos, true)
    this.scheduleDraft()
  }

  autoLayout() {
    const ws = useWorkspace.getState()
    if (!ws.solution || !ws.puzzleDoc) return
    const next = this.withLayout(ws.solution, ws.puzzleDoc.puzzle, true)
    ws.setPositions(next.layout, true)
    useUi.setState({ fitView: useUi.getState().fitView + 1 })
    this.scheduleDraft()
  }

  // --- hardware design ---------------------------------------------------------

  /** Adds a node of an allowed type with its default configuration. */
  addNode(type: string) {
    const ws = useWorkspace.getState()
    if (!ws.solution || !ws.hardwareCatalog?.some((e) => e.type === type)) return
    const id = nextNodeId(type, new Set(ws.cells.map((c) => c.id)))
    // Place it below the current design, near the middle.
    const pos = Object.values(ws.solution.layout)
    const x = pos.length ? (Math.min(...pos.map((p) => p.x)) + Math.max(...pos.map((p) => p.x))) / 2 : 0
    const y = pos.length ? Math.max(...pos.map((p) => p.y)) + NODE_HEIGHT + 40 : 0
    const free = (px: number) => !pos.some((p) => Math.abs(p.x - px) < NODE_WIDTH && Math.abs(p.y - y) < NODE_HEIGHT)
    let px = x
    for (let k = 1; !free(px) && k < 20; k++) px = x + (k % 2 ? 1 : -1) * Math.ceil(k / 2) * (NODE_WIDTH + 30)
    ws.addNode({ id, type }, { x: px, y }, this.programmable(type) ? '' : null)
    this.afterHardwareEdit()
    if (this.programmable(type)) this.scheduleAssemble(id)
    notify('info', `Added ${id}. Configure it in the inspector; connect its ports in the graph.`, 3000)
  }

  updateNode(id: string, patch: Parameters<ReturnType<typeof useWorkspace.getState>['updateNode']>[1]) {
    const removed = useWorkspace.getState().updateNode(id, patch)
    if (removed.length) {
      const list = removed.map((c) => `${c.from.cell}.${c.from.port}–${c.to.cell}.${c.to.port}`).join(', ')
      notify('warn', `Removed ${removed.length} connection${removed.length > 1 ? 's' : ''} that no longer fit ${id}: ${list}.`)
    }
    this.afterHardwareEdit()
    if (findCell(useWorkspace.getState().cells, id)?.programmable) this.scheduleAssemble(id)
  }

  removeNode(id: string) {
    const ws = useWorkspace.getState()
    if (!ws.solution?.hardware.some((n) => n.id === id)) return
    ws.removeNode(id)
    this.afterHardwareEdit()
    void this.pushBreakpoints()
  }

  private afterHardwareEdit() {
    this.invalidate()
    this.scheduleDraft()
    this.scheduleHardwareCheck()
  }

  /**
   * Asks the backend to validate and cost the current design (by loading
   * it), so constraint violations and costs show before anything runs.
   */
  private scheduleHardwareCheck(delay = HARDWARE_CHECK_MS) {
    if (!useWorkspace.getState().hardwareCatalog) return
    if (this.hardwareTimer) clearTimeout(this.hardwareTimer)
    this.hardwareTimer = setTimeout(() => {
      this.hardwareTimer = null
      void this.serial(() => this.syncSolution().catch(() => {}))
    }, delay)
  }

  /** Records the backend's hardware report and its hardware/topology problems. */
  private applyHardware(report: HardwareReport | undefined, diagnostics: Diagnostic[]) {
    if (!useWorkspace.getState().hardwareCatalog) return
    const issues = diagnostics.filter((d) => d.severity === 'error' && (d.code?.startsWith('solution.hardware') || d.path === 'hardware'))
    useWorkspace.getState().setHardwareReport(report ?? null, issues)
  }

  async loadBests(id: string) {
    try {
      const r = await http.getScores(id)
      if (useWorkspace.getState().puzzleDoc?.puzzle.id === id) useWorkspace.getState().setBests(r, null)
    } catch {
      /* bests are optional */
    }
  }

  /** Submits a passing solution; the server re-runs it and keeps per-dimension bests. */
  private async submitScore() {
    const ws = useWorkspace.getState()
    if (!ws.solution || !ws.puzzleDoc) return
    const id = ws.puzzleDoc.puzzle.id
    try {
      const r = await http.submitScore(id, serializeSolution(ws.solution))
      if (useWorkspace.getState().puzzleDoc?.puzzle.id !== id) return
      useWorkspace.getState().setBests(r.record, r)
      if (r.improved.length) notify('success', `New personal best: ${r.improved.join(', ')}.`, 4000)
      void this.refreshPuzzles()
    } catch (e) {
      notify('warn', `Score not recorded: ${errorText(e)}`)
    }
  }

  /**
   * Any edit that affects execution invalidates the running machine: the
   * backend still holds the old program, so the session is reset and the
   * next action reloads the solution.
   */
  private invalidate() {
    const sim = useSim.getState()
    if (sim.running) void this.call('pause').catch(() => {})
    if (sim.started) {
      this.epoch++
      this.stopPolling()
      useSim.setState({ started: false, running: false, view: emptyView, pause: null, edges: {} })
      notify('info', 'Simulation reset because the solution changed.', 2500)
    }
  }

  private scheduleAssemble(cell: string) {
    const t = this.assembleTimers.get(cell)
    if (t) clearTimeout(t)
    this.assembleTimers.set(
      cell,
      setTimeout(() => {
        this.assembleTimers.delete(cell)
        void this.assemble(cell)
      }, ASSEMBLE_DEBOUNCE_MS),
    )
  }

  /** Asks the real assembler for the exact size and diagnostics of one program. */
  async assemble(cell: string) {
    const ws = useWorkspace.getState()
    const def = findCell(ws.cells, cell)
    const source = ws.solution?.programs[cell]
    if (!def || source === undefined || this.rpc?.connectionState !== 'open') return
    try {
      const r = await this.call<AssembleResult>('assemble', { type: def.type, source, memoryLimit: def.programMemory, ports: def.portCount })
      // Ignore stale responses for sources that changed meanwhile.
      if (useWorkspace.getState().solution?.programs[cell] === source) ws.setAssemble(cell, { ...r, source })
    } catch {
      /* transient; the next edit or compile retries */
    }
  }

  assembleAll() {
    const sol = useWorkspace.getState().solution
    if (!sol) return
    for (const cell of Object.keys(sol.programs)) void this.assemble(cell)
  }

  private scheduleDraft() {
    if (this.draftTimer) clearTimeout(this.draftTimer)
    this.draftTimer = setTimeout(() => {
      const ws = useWorkspace.getState()
      if (ws.dirty && ws.solution) saveDraft(ws.solution.puzzle, serializeSolution(ws.solution), ws.solutionName)
    }, DRAFT_DEBOUNCE_MS)
  }

  async toggleBreakpoint(cell: string, line: number) {
    useWorkspace.getState().toggleBreakpoint(cell, line)
    await this.pushBreakpoints()
  }

  async pushBreakpoints() {
    if (this.rpc?.connectionState !== 'open') return
    try {
      await this.call('setBreakpoints', { breakpoints: useWorkspace.getState().breakpoints })
    } catch (e) {
      notify('warn', `Breakpoints not applied: ${errorText(e)}`)
    }
  }

  // --- backend sync & compilation ---------------------------------------------

  /** Loads the current solution into the backend session if it changed. */
  private async syncSolution() {
    const ws = useWorkspace.getState()
    if (!ws.solution) throw new Error('No puzzle loaded.')
    const version = ws.version
    if (useSim.getState().loadedVersion === version) return
    try {
      const r = await this.call<{ hardware?: HardwareReport }>('loadSolution', { yaml: serializeSolution(ws.solution) })
      if (useWorkspace.getState().version === version) this.applyHardware(r?.hardware, [])
    } catch (e) {
      if (e instanceof RpcError && useWorkspace.getState().version === version) {
        this.applyHardware((e.data as { hardware?: HardwareReport } | undefined)?.hardware, e.diagnostics)
      }
      throw e
    }
    useSim.setState({ loadedVersion: version, started: false })
  }

  /** Validates the topology and compiles every program with the Go assembler. */
  async compile(opts: { quiet?: boolean } = {}): Promise<boolean> {
    const ws = useWorkspace.getState()
    if (!ws.solution) return false
    if (ws.compile.state === 'ok' && ws.compile.version === ws.version && useSim.getState().loadedVersion === ws.version) return true
    const version = ws.version
    ws.setCompile({ state: 'running' })
    try {
      await this.syncSolution()
      const r = await this.call<CompileResult>('compile')
      const programs: Record<string, CompileResult['programs'][number]> = {}
      for (const p of r.programs) programs[p.cell] = p
      ws.setCompile({ state: 'ok', version, programs, totalBytes: r.totalBytes })
      ws.setDiagnostics(r.diagnostics)
      if (!opts.quiet) notify('success', `Compiled — ${r.totalBytes} program bytes.`, 2500)
      return true
    } catch (e) {
      ws.setCompile({ state: 'error', version })
      if (e instanceof RpcError && e.diagnostics.length > 0) {
        ws.setDiagnostics(e.diagnostics)
        useUi.setState({ bottomTab: 'diagnostics' })
        const n = e.diagnostics.filter((d) => d.severity === 'error').length
        notify('error', `${e.code === -32001 ? 'Solution is invalid' : 'Compilation failed'}: ${n} error${n === 1 ? '' : 's'}.`)
      } else {
        notify('error', `Compile failed: ${errorText(e)}`)
      }
      return false
    }
  }

  // --- execution ------------------------------------------------------------------

  private needsStart(): boolean {
    const sim = useSim.getState()
    return !sim.started || isTerminal(sim.view.status) || useSim.getState().loadedVersion !== useWorkspace.getState().version
  }

  private async _start(testIndex = useSim.getState().testIndex): Promise<boolean> {
    if (!(await this.compile({ quiet: true }))) return false
    try {
      this.stopPolling()
      await this.pushBreakpoints()
      const snap = await this.call<Snapshot>('start', { test: testIndex, cycleDetection: usePrefs.getState().cycleDetection })
      this.epoch++
      this.ring.clear()
      useSim.setState({
        started: true,
        running: false,
        testIndex,
        view: viewFromSnapshot(snap),
        pause: null,
        edges: {},
        log: [],
      })
      return true
    } catch (e) {
      notify('error', `Cannot start: ${errorText(e)}`)
      return false
    }
  }

  private async _step() {
    if (useSim.getState().running) return
    if (this.needsStart() && !(await this._start())) return
    try {
      await this.setLevel('all')
      await this.call('step', { ticks: 1 })
      useSim.setState({ pause: null })
      await this.resync()
    } catch (e) {
      notify('error', `Step failed: ${errorText(e)}`)
    }
  }

  private async _run() {
    if (useSim.getState().running) return
    if (this.needsStart() && !(await this._start())) return
    const speed = usePrefs.getState().speed
    const delayMs = SPEEDS.find((s) => s.id === speed)?.delayMs ?? 0
    try {
      // At maximum speed the engine runs far faster than the browser can
      // draw; send only lifecycle events and sample snapshots instead.
      await this.setLevel(speed === 'max' ? 'lifecycle' : 'all')
      await this.call('run', { delayMs })
      useSim.setState({ running: true, pause: null })
      if (speed === 'max' || delayMs < 100) this.startPolling()
    } catch (e) {
      notify('error', `Run failed: ${errorText(e)}`)
    }
  }

  private async _pause() {
    if (!useSim.getState().running) return
    try {
      await this.call('pause')
    } catch (e) {
      notify('warn', `Pause failed: ${errorText(e)}`)
    }
    this.stopPolling()
    useSim.setState({ running: false })
    await this.resync()
  }

  private async _reset() {
    if (!useSim.getState().started) return
    this.stopPolling()
    try {
      const snap = await this.call<Snapshot>('reset')
      this.epoch++
      this.ring.clear()
      useSim.setState({ running: false, view: viewFromSnapshot(snap), pause: null, edges: {}, log: [] })
    } catch (e) {
      notify('error', `Reset failed: ${errorText(e)}`)
    }
  }

  private async _selectTest(index: number) {
    useSim.setState({ testIndex: index })
    if (useSim.getState().started) await this._start(index)
  }

  async setSpeed(speed: (typeof SPEEDS)[number]['id']) {
    usePrefs.getState().set({ speed })
    if (useSim.getState().running) {
      await this.serial(async () => {
        await this._pause()
        await this._run()
      })
    }
  }

  private async _runTests(): Promise<SuiteResult | null> {
    if (!(await this.compile({ quiet: true }))) return null
    const tests = useWorkspace.getState().puzzleDoc?.puzzle.tests.map((t) => t.name) ?? []
    useSim.setState({ testsRunning: tests })
    useUi.setState({ bottomTab: 'tests' })
    try {
      const r = await this.call<SuiteResult>('runTests', { cycleDetection: usePrefs.getState().cycleDetection })
      const results: Record<string, TestResult> = {}
      for (const t of r.tests) results[t.test] = t
      useSim.setState({ results })
      if (r.passed) {
        markSolved(r.puzzle)
        void this.submitScore()
        notify('success', `All ${r.summary.totalTests} tests passed — ${r.summary.totalProgramBytes} bytes, ${r.summary.maxTicks} ticks max.`)
      } else {
        notify('error', `${r.summary.totalTests - r.summary.passedTests} of ${r.summary.totalTests} tests failed.`)
      }
      return r
    } catch (e) {
      notify('error', `Running tests failed: ${errorText(e)}`)
      return null
    } finally {
      useSim.setState({ testsRunning: [] })
    }
  }

  private async _runTest(name: string): Promise<TestResult | null> {
    if (!(await this.compile({ quiet: true }))) return null
    useSim.setState((s) => ({ testsRunning: [...s.testsRunning, name] }))
    try {
      const r = await this.call<TestResult>('runTest', { test: name, cycleDetection: usePrefs.getState().cycleDetection })
      useSim.setState((s) => ({ results: { ...s.results, [r.test]: r } }))
      return r
    } catch (e) {
      notify('error', `Test "${name}" failed to run: ${errorText(e)}`)
      return null
    } finally {
      useSim.setState((s) => ({ testsRunning: s.testsRunning.filter((t) => t !== name) }))
    }
  }

  // --- serialized public execution API ------------------------------------------
  start(testIndex?: number): Promise<boolean> {
    return this.serial(() => this._start(testIndex))
  }
  step(): Promise<void> {
    return this.serial(() => this._step())
  }
  run(): Promise<void> {
    return this.serial(() => this._run())
  }
  pause(): Promise<void> {
    return this.serial(() => this._pause())
  }
  reset(): Promise<void> {
    return this.serial(() => this._reset())
  }
  selectTest(index: number): Promise<void> {
    return this.serial(() => this._selectTest(index))
  }
  runTests(): Promise<SuiteResult | null> {
    return this.serial(() => this._runTests())
  }
  runTest(name: string): Promise<TestResult | null> {
    return this.serial(() => this._runTest(name))
  }

  // --- persistence ------------------------------------------------------------------

  async save(name?: string): Promise<boolean> {
    const ws = useWorkspace.getState()
    if (!ws.solution) return false
    const target = name ?? ws.solutionName
    if (!target) return false
    try {
      await http.saveSolution(ws.solution.puzzle, target, serializeSolution(ws.solution))
      useWorkspace.getState().markSaved(target)
      clearDraft(ws.solution.puzzle)
      notify('success', `Saved "${target}".`, 2000)
      void this.refreshPuzzles()
      return true
    } catch (e) {
      const detail = e instanceof ApiError && e.diagnostics.length ? ` (${e.diagnostics[0].message})` : ''
      notify('error', `Save failed: ${errorText(e)}${detail}`)
      return false
    }
  }

  async loadSolution(name: string): Promise<boolean> {
    const ws = useWorkspace.getState()
    const doc = ws.puzzleDoc
    if (!doc) return false
    try {
      const s = await http.getSolution(doc.puzzle.id, name)
      const { solution, warnings } = adaptToPuzzle(parseSolution(s.yaml), doc.puzzle, this.programmable)
      this.applySolution(solution, name, false)
      warnings.forEach((w) => notify('warn', w))
      clearDraft(doc.puzzle.id)
      return true
    } catch (e) {
      notify('error', `Cannot load "${name}": ${errorText(e)}`)
      return false
    }
  }

  /** Loads the personal-best solution for one score dimension (unsaved). */
  async loadBest(dimension: 'cost' | 'ticks' | 'bytes'): Promise<boolean> {
    const doc = useWorkspace.getState().puzzleDoc
    if (!doc) return false
    try {
      const s = await http.getBestSolution(doc.puzzle.id, dimension)
      const { solution, warnings } = adaptToPuzzle(parseSolution(s.yaml), doc.puzzle, this.programmable)
      this.applySolution(solution, '', true)
      warnings.forEach((w) => notify('warn', w))
      notify('info', `Loaded your best ${dimension} solution. Save it to keep a named copy.`)
      return true
    } catch (e) {
      notify('error', `Cannot load the best ${dimension} solution: ${errorText(e)}`)
      return false
    }
  }

  newSolution() {
    const doc = useWorkspace.getState().puzzleDoc
    if (!doc) return
    this.applySolution(emptySolution(doc.puzzle, this.programmable), '', false)
    clearDraft(doc.puzzle.id)
  }

  /** Imports solution YAML text. Throws with a readable message on failure. */
  importYaml(text: string) {
    const doc = useWorkspace.getState().puzzleDoc
    if (!doc) throw new Error('Open a puzzle first.')
    const parsed = parseSolution(text)
    if (parsed.puzzle !== doc.puzzle.id) {
      throw new Error(`This solution is for puzzle "${parsed.puzzle}", but "${doc.puzzle.id}" is open.`)
    }
    const { solution, warnings } = adaptToPuzzle(parsed, doc.puzzle, this.programmable)
    this.applySolution(solution, '', true)
    warnings.forEach((w) => notify('warn', w))
    notify('success', 'Solution imported. Save it to keep it on the server.')
  }

  exportYaml(): { filename: string; yaml: string } | null {
    const ws = useWorkspace.getState()
    if (!ws.solution) return null
    return { filename: `${ws.solution.puzzle}-${ws.solutionName || 'solution'}.yaml`, yaml: serializeSolution(ws.solution) }
  }

  private applySolution(model: SolutionModel, name: string, dirty: boolean) {
    const doc = useWorkspace.getState().puzzleDoc
    if (!doc) return
    const withPos = this.withLayout(model, doc.puzzle)
    this.invalidate()
    useWorkspace.getState().replaceSolution(withPos, name, dirty)
    useSim.setState({ results: {} })
    this.assembleAll()
    this.scheduleHardwareCheck(0)
    if (dirty) this.scheduleDraft()
    useUi.setState({ fitView: useUi.getState().fitView + 1 })
  }
}

export function errorText(e: unknown): string {
  if (e instanceof RpcError || e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return String(e)
}

/** The application-wide controller instance. */
export let controller = new Controller()

/** Replaces the controller (tests). */
export function setController(c: Controller) {
  controller = c
}
