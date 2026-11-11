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
  Endpoint,
  ProgramReport,
  PuzzleDocument,
  PuzzleSummary,
} from '../api/types'
import { checkConnection, connectionId, findConnection, type ConnectCheck } from '../lib/connections'
import type { Position, SolutionModel } from '../lib/solution'

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
}

const emptyCompile: CompileState = { state: 'idle', version: -1, programs: {}, totalBytes: 0 }

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

  setCatalog: (catalog) => set({ catalog }),
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
    }))
  },
  replaceSolution: (solution, solutionName, dirty) =>
    set((s) => ({ solution, solutionName, dirty, version: s.version + 1, assemble: {}, compile: emptyCompile, diagnostics: [], selectedConnection: null })),
  setProgram: (cell, source) => {
    const sol = get().solution
    if (!sol || sol.programs[cell] === source) return
    set((s) => ({ solution: { ...sol, programs: { ...sol.programs, [cell]: source } }, dirty: true, version: s.version + 1 }))
  },
  connect: (a, b) => {
    const { solution, puzzleDoc } = get()
    if (!solution || !puzzleDoc) return { ok: false, reason: 'No puzzle loaded.' }
    const check = checkConnection(puzzleDoc.puzzle.cells, solution.connections, a, b)
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

/** Stream binding of an input/output cell (generic: the "stream" config key). */
export function cellStream(cell: CellDef): string | undefined {
  const s = cell.config?.stream
  return typeof s === 'string' ? s : undefined
}

/**
 * Whether a non-programmable cell feeds a test input stream or collects a
 * test output stream. Built-in types are recognised by name; plugin cells
 * by whether their configured stream appears in the tests' inputs or outputs.
 */
export function streamRole(cell: CellDef, tests: { inputs: Record<string, unknown>; outputs: Record<string, unknown> }[] = []): 'input' | 'output' | undefined {
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
