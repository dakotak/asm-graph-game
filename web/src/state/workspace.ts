// Editing state: the loaded puzzle, the solution being edited, selection,
// live assembler feedback, compile results, diagnostics and breakpoints.
// Execution state lives in ./sim so simulation updates never re-render the
// editor.
import { create } from 'zustand'
import type {
  AssembleResult,
  CellDef,
  CellTypeCatalog,
  CellTypeInfo,
  Connection,
  Diagnostic,
  EffectiveHardware,
  Endpoint,
  HardwareReport,
  ProgramReport,
  PuzzleDocument,
  PuzzleSummary,
  ScoreRecord,
  ScoreResponse,
} from '../api/types'
import { checkConnection, connectionId, findConnection, type ConnectCheck, type DirectionOf } from '../lib/connections'
import { portDirection, portIndex, resolveCells, type CellView } from '../lib/hardware'
import type { HardwareNode, Position, SolutionModel } from '../lib/solution'

export interface CompileState {
  state: 'idle' | 'running' | 'ok' | 'error'
  /** solution version that was compiled */
  version: number
  programs: Record<string, ProgramReport>
  totalBytes: number
}

export interface WorkspaceState {
  catalog: CellTypeCatalog | null
  puzzles: PuzzleSummary[]
  puzzleDoc: PuzzleDocument | null
  solution: SolutionModel | null
  /** saved solution file name, or '' for a never-saved solution */
  solutionName: string
  dirty: boolean
  /** increments on every edit that changes execution (programs, connections) */
  version: number
  selectedCell: string | null
  selectedConnection: string | null
  /** live per-cell assembler results (from the backend `assemble` method) */
  assemble: Record<string, AssembleResult & { source: string }>
  compile: CompileState
  diagnostics: Diagnostic[]
  breakpoints: Record<string, number[]>
  /** every cell (fixed, then player-built), resolved; derived state */
  cells: CellView[]
  /** types players may build in this puzzle (null: fixed topology) */
  hardwareCatalog: EffectiveHardware[] | null
  /** the backend's last hardware report (costs, budget) */
  hardwareReport: HardwareReport | null
  /** the backend's hardware/topology diagnostics for the current design */
  hardwareIssues: Diagnostic[]
  /** personal bests for the open puzzle */
  bests: ScoreRecord | null
  /** the last scoring submission's result */
  lastScore: ScoreResponse | null

  setCatalog(c: CellTypeCatalog): void
  setPuzzles(p: PuzzleSummary[]): void
  openPuzzle(doc: PuzzleDocument, solution: SolutionModel, name: string, dirty: boolean): void
  replaceSolution(solution: SolutionModel, name: string, dirty: boolean): void
  setProgram(cell: string, source: string): void
  connect(a: Endpoint, b: Endpoint): ConnectCheck
  disconnect(id: string): void
  setPositions(pos: Record<string, Position>, markDirty: boolean): void
  select(cell: string | null): void
  selectConnection(id: string | null): void
  setAssemble(cell: string, r: AssembleResult & { source: string }): void
  setCompile(c: Partial<CompileState>): void
  setDiagnostics(d: Diagnostic[]): void
  toggleBreakpoint(cell: string, line: number): void
  setBreakpoints(cell: string, lines: number[]): void
  markSaved(name: string): void
  setHardwareCatalog(c: EffectiveHardware[] | null): void
  setHardwareReport(r: HardwareReport | null, issues: Diagnostic[]): void
  addNode(node: HardwareNode, position: Position, program: string | null): void
  /** Applies a change to a node; returns the connections it had to remove. */
  updateNode(id: string, patch: Partial<HardwareNode>): Connection[]
  removeNode(id: string): void
  setBests(r: ScoreRecord | null, last?: ScoreResponse | null): void
}

const emptyCompile: CompileState = { state: 'idle', version: -1, programs: {}, totalBytes: 0 }

type Derivable = Pick<WorkspaceState, 'puzzleDoc' | 'solution' | 'catalog' | 'hardwareCatalog' | 'hardwareReport'>

function derive(s: Derivable): CellView[] {
  return s.puzzleDoc ? resolveCells(s.puzzleDoc.puzzle, s.solution, s.catalog, s.hardwareCatalog, s.hardwareReport) : []
}

/** Port directions for connection checks (stream roles come from the tests). */
export function directionOf(s: Pick<WorkspaceState, 'catalog' | 'puzzleDoc'>): DirectionOf {
  const tests = s.puzzleDoc?.puzzle.tests ?? []
  return (cell, port) => portDirection(cell, port, s.catalog, cell.category === 'io' || !cell.programmable ? streamRole(cell, tests) : undefined)
}

/** Connections that no longer fit the cells (removed ports, incompatible directions). */
function invalidConnections(conns: Connection[], cells: CellView[], dir: DirectionOf): Connection[] {
  return conns.filter((c) => {
    const a = cells.find((x) => x.id === c.from.cell)
    const b = cells.find((x) => x.id === c.to.cell)
    if (!a || !b) return true
    const ia = portIndex(c.from.port)
    const ib = portIndex(c.to.port)
    if (ia < 0 || ib < 0 || ia >= a.portCount || ib >= b.portCount) return true
    const da = dir(a, c.from.port)
    return da !== 'both' && da === dir(b, c.to.port)
  })
}

export const useWorkspace = create<WorkspaceState>()((set, get) => ({
  catalog: null,
  puzzles: [],
  puzzleDoc: null,
  solution: null,
  solutionName: '',
  dirty: false,
  version: 0,
  selectedCell: null,
  selectedConnection: null,
  assemble: {},
  compile: emptyCompile,
  diagnostics: [],
  breakpoints: {},
  cells: [],
  hardwareCatalog: null,
  hardwareReport: null,
  hardwareIssues: [],
  bests: null,
  lastScore: null,

  setCatalog: (catalog) => set((s) => ({ catalog, cells: derive({ ...s, catalog }) })),
  setPuzzles: (puzzles) => set({ puzzles }),
  openPuzzle: (puzzleDoc, solution, solutionName, dirty) => {
    const firstProgrammable = Object.keys(solution.programs)[0] ?? null
    set((s) => ({
      puzzleDoc,
      solution,
      solutionName,
      dirty,
      version: s.version + 1,
      selectedCell: firstProgrammable,
      selectedConnection: null,
      assemble: {},
      compile: emptyCompile,
      diagnostics: [],
      breakpoints: {},
      hardwareReport: null,
      hardwareIssues: [],
      lastScore: null,
      cells: derive({ ...s, puzzleDoc, solution, hardwareReport: null }),
    }))
  },
  replaceSolution: (solution, solutionName, dirty) =>
    set((s) => ({
      solution,
      solutionName,
      dirty,
      version: s.version + 1,
      assemble: {},
      compile: emptyCompile,
      diagnostics: [],
      selectedConnection: null,
      hardwareReport: null,
      hardwareIssues: [],
      selectedCell: s.selectedCell && (s.cells.some((c) => c.id === s.selectedCell && !c.player) || solution.hardware.some((n) => n.id === s.selectedCell)) ? s.selectedCell : null,
      cells: derive({ ...s, solution, hardwareReport: null }),
    })),
  setProgram: (cell, source) => {
    const sol = get().solution
    if (!sol || sol.programs[cell] === source) return
    set((s) => ({ solution: { ...sol, programs: { ...sol.programs, [cell]: source } }, dirty: true, version: s.version + 1 }))
  },
  connect: (a, b) => {
    const { solution, puzzleDoc } = get()
    if (!solution || !puzzleDoc) return { ok: false, reason: 'No puzzle loaded.' }
    const check = checkConnection(get().cells, solution.connections, a, b, directionOf(get()))
    if (!check.ok) return check
    const conn: Connection = { from: a, to: b }
    set((s) => ({ solution: { ...solution, connections: [...solution.connections, conn] }, dirty: true, version: s.version + 1 }))
    return check
  },
  disconnect: (id) => {
    const sol = get().solution
    if (!sol) return
    const connections = sol.connections.filter((c) => connectionId(c) !== id)
    if (connections.length === sol.connections.length) return
    set((s) => ({
      solution: { ...sol, connections },
      dirty: true,
      version: s.version + 1,
      selectedConnection: s.selectedConnection === id ? null : s.selectedConnection,
    }))
  },
  setPositions: (pos, markDirty) => {
    const sol = get().solution
    if (!sol) return
    set((s) => ({ solution: { ...sol, layout: { ...sol.layout, ...pos } }, dirty: s.dirty || markDirty }))
  },
  select: (selectedCell) => set({ selectedCell }),
  selectConnection: (selectedConnection) => set({ selectedConnection }),
  setAssemble: (cell, r) => set((s) => ({ assemble: { ...s.assemble, [cell]: r } })),
  setCompile: (c) => set((s) => ({ compile: { ...s.compile, ...c } })),
  setDiagnostics: (diagnostics) => set({ diagnostics }),
  toggleBreakpoint: (cell, line) =>
    set((s) => {
      const cur = s.breakpoints[cell] ?? []
      const next = cur.includes(line) ? cur.filter((l) => l !== line) : [...cur, line].sort((a, b) => a - b)
      return { breakpoints: { ...s.breakpoints, [cell]: next } }
    }),
  setBreakpoints: (cell, lines) => set((s) => ({ breakpoints: { ...s.breakpoints, [cell]: lines } })),
  markSaved: (solutionName) => set({ solutionName, dirty: false }),
  setHardwareCatalog: (hardwareCatalog) => set((s) => ({ hardwareCatalog, cells: derive({ ...s, hardwareCatalog }) })),
  setHardwareReport: (hardwareReport, hardwareIssues) => set((s) => ({ hardwareReport, hardwareIssues, cells: derive({ ...s, hardwareReport }) })),
  addNode: (node, position, program) => {
    const sol = get().solution
    if (!sol) return
    const solution: SolutionModel = {
      ...sol,
      hardware: [...sol.hardware, node],
      layout: { ...sol.layout, [node.id]: position },
      programs: program === null ? sol.programs : { ...sol.programs, [node.id]: program },
    }
    set((s) => ({ solution, dirty: true, version: s.version + 1, selectedCell: node.id, selectedConnection: null, cells: derive({ ...s, solution }) }))
  },
  updateNode: (id, patch) => {
    const sol = get().solution
    if (!sol) return []
    const hardware = sol.hardware.map((n) => {
      if (n.id !== id) return n
      const next: HardwareNode = { ...n, ...patch }
      for (const k of Object.keys(patch) as (keyof HardwareNode)[]) if (patch[k] === undefined) delete next[k]
      return next
    })
    let solution: SolutionModel = { ...sol, hardware }
    const cells = derive({ ...get(), solution })
    // Connections to ports that no longer exist (or can no longer carry
    // data) are removed explicitly, and reported to the caller.
    const removed = invalidConnections(
      sol.connections.filter((c) => c.from.cell === id || c.to.cell === id),
      cells,
      directionOf(get()),
    )
    if (removed.length) solution = { ...solution, connections: sol.connections.filter((c) => !removed.includes(c)) }
    set((s) => ({ solution, dirty: true, version: s.version + 1, cells: derive({ ...s, solution }) }))
    return removed
  },
  removeNode: (id) => {
    const sol = get().solution
    if (!sol || !sol.hardware.some((n) => n.id === id)) return
    const programs = { ...sol.programs }
    delete programs[id]
    const layout = { ...sol.layout }
    delete layout[id]
    const solution: SolutionModel = {
      ...sol,
      hardware: sol.hardware.filter((n) => n.id !== id),
      connections: sol.connections.filter((c) => c.from.cell !== id && c.to.cell !== id),
      programs,
      layout,
    }
    set((s) => {
      const breakpoints = { ...s.breakpoints }
      delete breakpoints[id]
      const assemble = { ...s.assemble }
      delete assemble[id]
      return {
        solution,
        dirty: true,
        version: s.version + 1,
        breakpoints,
        assemble,
        selectedCell: s.selectedCell === id ? null : s.selectedCell,
        selectedConnection: null,
        cells: derive({ ...s, solution }),
      }
    })
  },
  setBests: (bests, last) => set((s) => ({ bests, lastScore: last === undefined ? s.lastScore : last })),
}))

// --- selectors & helpers --------------------------------------------------

export function cellType(catalog: CellTypeCatalog | null, type: string): CellTypeInfo | undefined {
  return catalog?.types.find((t) => t.type === type)
}

export function isProgrammableType(catalog: CellTypeCatalog | null, type: string): boolean {
  return cellType(catalog, type)?.programmable ?? false
}

export function puzzleCells(): CellDef[] {
  return useWorkspace.getState().puzzleDoc?.puzzle.cells ?? []
}

/** The resolved cell with this id (fixed or player-built). */
export function findCell(cells: CellView[], id: string | null | undefined): CellView | undefined {
  return id ? cells.find((c) => c.id === id) : undefined
}

/** Stream binding of an input/output cell (generic: the "stream" config key). */
export function cellStream(cell: { config?: Record<string, unknown> }): string | undefined {
  const s = cell.config?.stream
  return typeof s === 'string' ? s : undefined
}

/**
 * Whether a non-programmable cell feeds a test input stream or collects a
 * test output stream. Built-in types are recognised by name; plugin cells
 * by whether their configured stream appears in the tests' inputs or outputs.
 */
export function streamRole(cell: { type: string; config?: Record<string, unknown> }, tests: { inputs: Record<string, unknown>; outputs: Record<string, unknown> }[] = []): 'input' | 'output' | undefined {
  if (cell.type === 'input') return 'input'
  if (cell.type === 'output') return 'output'
  const stream = cellStream(cell)
  if (!stream) return undefined
  if (tests.some((t) => stream in t.inputs)) return 'input'
  if (tests.some((t) => stream in t.outputs)) return 'output'
  return undefined
}

export function findConnectionIndex(a: Endpoint, b: Endpoint): number {
  return findConnection(useWorkspace.getState().solution?.connections ?? [], a, b)
}
