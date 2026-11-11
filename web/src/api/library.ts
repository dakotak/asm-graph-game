// The puzzle library and the player's saved work, kept in the browser.
//
// There is no server: puzzles are bundled with the app (examples/puzzles)
// or imported by the player, and saved solutions, personal bests and
// imported puzzles live in browser storage. Everything that needs the game
// rules — parsing and validating puzzles, running test suites, computing
// scores — is done by the engine (Go compiled to WebAssembly) through the
// `call` function, exactly as the server used to do it.
import type {
  Diagnostic,
  Puzzle,
  PuzzleDocument,
  PuzzleSummary,
  Score,
  ScoreDimension,
  ScoreRecord,
  ScoreResponse,
  SolutionInfo,
  SuiteResult,
} from './types'
import { RpcError } from './rpc'
import { parseSolution } from '../lib/solution'

export class ApiError extends Error {
  readonly status: number
  readonly diagnostics: Diagnostic[]
  constructor(message: string, status: number, diagnostics: Diagnostic[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.diagnostics = diagnostics
  }
}

/** Solution names accepted by the library. */
export const SOLUTION_NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/

export function isValidSolutionName(name: string): boolean {
  return SOLUTION_NAME_RE.test(name) && !name.includes('..')
}

export interface Library {
  listPuzzles(): Promise<PuzzleSummary[]>
  getPuzzle(id: string): Promise<PuzzleDocument>
  listSolutions(puzzleId: string): Promise<SolutionInfo[]>
  getSolution(puzzleId: string, name: string): Promise<{ name: string; yaml: string }>
  saveSolution(puzzleId: string, name: string, yaml: string): Promise<{ name: string; saved: boolean }>
  deleteSolution(puzzleId: string, name: string): Promise<void>
  getScores(puzzleId: string): Promise<ScoreRecord>
  /** Runs every test in the engine and records the score if all pass. */
  submitScore(puzzleId: string, yaml: string): Promise<ScoreResponse>
  getBestSolution(puzzleId: string, dimension: ScoreDimension): Promise<{ name: string; yaml: string }>
  /** Adds (or replaces) a player-imported puzzle; returns its summary. */
  importPuzzle(file: string, yaml: string): Promise<PuzzleSummary>
  removePuzzle(id: string): Promise<void>
}

export type EngineCall = <T>(method: string, params?: unknown) => Promise<T>

/** A minimal key/value store (localStorage-compatible). */
export interface KeyValue {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

interface Entry {
  file: string
  yaml: string
  builtin: boolean
}

interface SavedSolution {
  yaml: string
  modified: string
}

interface StoredBests extends ScoreRecord {
  /** dimension -> solution YAML that achieved the best */
  solutions: Partial<Record<ScoreDimension, string>>
}

const DIMENSIONS: ScoreDimension[] = ['cost', 'ticks', 'bytes']
const MAX_SOLUTION_BYTES = 1 << 20

/** Mirrors scoring.Better: lower wins; ties broken by cost, ticks, bytes. */
export function better(a: Score, b: Score, d: ScoreDimension): boolean {
  if (a[d] !== b[d]) return a[d] < b[d]
  for (const o of DIMENSIONS) {
    if (o !== d && a[o] !== b[o]) return a[o] < b[o]
  }
  return false
}

/** The puzzle files shipped with the app (examples/puzzles). */
export function bundledPuzzles(): { file: string; yaml: string }[] {
  const files = import.meta.glob('../../../examples/puzzles/*.yaml', { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  return Object.entries(files)
    .map(([path, yaml]) => ({ file: path.slice(path.lastIndexOf('/') + 1), yaml }))
    .sort((a, b) => a.file.localeCompare(b.file))
}

export class BrowserLibrary implements Library {
  private summaries = new Map<string, { summary: PuzzleSummary; puzzle: Puzzle | null; yaml: string }>()

  constructor(
    private readonly call: EngineCall,
    private readonly store: KeyValue,
    private readonly builtin: { file: string; yaml: string }[] = bundledPuzzles(),
    private readonly now: () => Date = () => new Date(),
  ) {}

  // --- storage helpers -------------------------------------------------------

  private read<T>(key: string, fallback: T): T {
    try {
      const raw = this.store.getItem(key)
      return raw ? (JSON.parse(raw) as T) : fallback
    } catch {
      return fallback
    }
  }

  private write(key: string, value: unknown) {
    try {
      this.store.setItem(key, JSON.stringify(value))
    } catch (e) {
      throw new ApiError(`Browser storage is full or unavailable: ${(e as Error).message}`, 507)
    }
  }

  private imported(): Record<string, { file: string; yaml: string }> {
    return this.read('asmgraph.puzzles', {})
  }

  private solutions(id: string): Record<string, SavedSolution> {
    return this.read(`asmgraph.solutions.${id}`, {})
  }

  private bests(id: string): StoredBests {
    return this.read<StoredBests>(`asmgraph.bests.${id}`, { puzzle: id, bests: {}, solutions: {} })
  }

  // --- puzzles -----------------------------------------------------------------

  private async summarize(e: Entry) {
    const cached = this.summaries.get(e.file)
    if (cached && cached.yaml === e.yaml) return cached
    const r = await this.call<{ summary: PuzzleSummary; puzzle: Puzzle | null }>('summarizePuzzle', { yaml: e.yaml, file: e.file })
    const v = { summary: r.summary, puzzle: r.puzzle, yaml: e.yaml }
    this.summaries.set(e.file, v)
    return v
  }

  private entries(): Entry[] {
    return [
      ...this.builtin.map((b) => ({ ...b, builtin: true })),
      ...Object.values(this.imported()).map((p) => ({ ...p, builtin: false })),
    ]
  }

  async listPuzzles(): Promise<PuzzleSummary[]> {
    const out: PuzzleSummary[] = []
    const seen = new Set<string>()
    for (const e of this.entries()) {
      const { summary } = await this.summarize(e)
      if (seen.has(summary.id)) continue // the first file wins for duplicate ids
      seen.add(summary.id)
      const bests = this.bests(summary.id).bests
      out.push({
        ...summary,
        solutions: Object.keys(this.solutions(summary.id)).length,
        bests: Object.keys(bests).length ? bests : undefined,
        imported: !e.builtin,
      } as PuzzleSummary)
    }
    return out
  }

  async getPuzzle(id: string): Promise<PuzzleDocument> {
    for (const e of this.entries()) {
      const s = await this.summarize(e)
      if (s.summary.id === id) {
        if (!s.puzzle) throw new ApiError(`Puzzle "${id}" does not parse.`, 422, s.summary.diagnostics ?? [])
        return { summary: s.summary, yaml: s.yaml, puzzle: s.puzzle }
      }
    }
    throw new ApiError(`No puzzle "${id}".`, 404)
  }

  async importPuzzle(file: string, yaml: string): Promise<PuzzleSummary> {
    if (yaml.length > 4 << 20) throw new ApiError('Puzzle files are limited to 4 MiB.', 413)
    const { summary, puzzle } = await this.summarize({ file: `imported/${file}`, yaml, builtin: false })
    this.summaries.delete(`imported/${file}`)
    if (!puzzle) throw new ApiError('The file is not a valid puzzle.', 422, summary.diagnostics ?? [])
    for (const b of this.builtin) {
      if ((await this.summarize({ ...b, builtin: true })).summary.id === summary.id) {
        throw new ApiError(`A built-in puzzle already uses the id "${summary.id}". Change the id to import it.`, 409)
      }
    }
    const all = this.imported()
    all[summary.id] = { file: `imported/${file}`, yaml }
    this.write('asmgraph.puzzles', all)
    return summary
  }

  async removePuzzle(id: string): Promise<void> {
    const all = this.imported()
    delete all[id]
    this.write('asmgraph.puzzles', all)
  }

  // --- solutions ---------------------------------------------------------------

  async listSolutions(puzzleId: string): Promise<SolutionInfo[]> {
    return Object.entries(this.solutions(puzzleId))
      .map(([name, s]) => ({ name, modified: s.modified, size: s.yaml.length }))
      .sort((a, b) => a.name.localeCompare(b.name))
  }

  async getSolution(puzzleId: string, name: string) {
    const s = this.solutions(puzzleId)[name]
    if (!s) throw new ApiError(`No saved solution "${name}".`, 404)
    return { name, yaml: s.yaml }
  }

  async saveSolution(puzzleId: string, name: string, yaml: string) {
    if (!isValidSolutionName(name)) throw new ApiError("Invalid solution name (letters, digits, '.', '_', '-'; max 64).", 400)
    if (yaml.length > MAX_SOLUTION_BYTES) throw new ApiError('Solutions are limited to 1 MiB.', 413)
    // Work in progress may not compile, but it must be a solution for this puzzle.
    const parsed = parseSolution(yaml)
    if (parsed.puzzle !== puzzleId) throw new ApiError(`The solution is for puzzle "${parsed.puzzle}", not "${puzzleId}".`, 422)
    const all = this.solutions(puzzleId)
    all[name] = { yaml, modified: this.now().toISOString() }
    this.write(`asmgraph.solutions.${puzzleId}`, all)
    return { name, saved: true }
  }

  async deleteSolution(puzzleId: string, name: string) {
    const all = this.solutions(puzzleId)
    delete all[name]
    this.write(`asmgraph.solutions.${puzzleId}`, all)
  }

  // --- scores --------------------------------------------------------------------

  async getScores(puzzleId: string): Promise<ScoreRecord> {
    const b = this.bests(puzzleId)
    return { puzzle: puzzleId, bests: b.bests }
  }

  async submitScore(puzzleId: string, yaml: string): Promise<ScoreResponse> {
    const doc = await this.getPuzzle(puzzleId)
    let res: SuiteResult
    try {
      res = await this.call<SuiteResult>('evaluateSolution', { puzzle: doc.yaml, solution: yaml })
    } catch (e) {
      if (e instanceof RpcError) throw new ApiError(e.message, 422, e.diagnostics)
      throw e
    }
    const stored = this.bests(puzzleId)
    const improved: ScoreDimension[] = []
    if (res.passed && res.score) {
      for (const d of DIMENSIONS) {
        const cur = stored.bests[d]
        if (!cur || better(res.score, cur.score, d)) {
          improved.push(d)
          stored.bests[d] = { score: res.score, file: `best-${d}.yaml`, achieved: this.now().toISOString() }
          stored.solutions[d] = yaml
        }
      }
      if (improved.length) this.write(`asmgraph.bests.${puzzleId}`, stored)
    }
    return { passed: res.passed, score: res.score, improved, record: { puzzle: puzzleId, bests: stored.bests } }
  }

  async getBestSolution(puzzleId: string, dimension: ScoreDimension) {
    const yaml = this.bests(puzzleId).solutions[dimension]
    if (!yaml) throw new ApiError(`No personal best for ${dimension}.`, 404)
    return { name: `best-${dimension}`, yaml }
  }
}
