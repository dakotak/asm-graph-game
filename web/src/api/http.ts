// HTTP client for the library endpoints of `asmgraph web` (transport/web).
import type { Diagnostic, PuzzleDocument, PuzzleSummary, ScoreDimension, ScoreRecord, ScoreResponse, SolutionInfo } from './types'

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

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, {
      ...init,
      headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
    })
  } catch {
    throw new ApiError('Cannot reach the asmgraph server. Is `asmgraph web` running?', 0)
  }
  let body: unknown = null
  const text = await resp.text()
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      throw new ApiError(`Unexpected response from server (HTTP ${resp.status})`, resp.status)
    }
  }
  if (!resp.ok) {
    const b = (body ?? {}) as { error?: string; diagnostics?: Diagnostic[] }
    throw new ApiError(b.error ?? `HTTP ${resp.status}`, resp.status, b.diagnostics ?? [])
  }
  return body as T
}

const enc = encodeURIComponent

export const http = {
  health: () => request<{ ok: boolean; protocolVersion: string; version: string }>('/api/health'),
  listPuzzles: async () => (await request<{ puzzles: PuzzleSummary[] }>('/api/puzzles')).puzzles,
  getPuzzle: (id: string) => request<PuzzleDocument>(`/api/puzzles/${enc(id)}`),
  listSolutions: async (puzzleId: string) =>
    (await request<{ solutions: SolutionInfo[] }>(`/api/puzzles/${enc(puzzleId)}/solutions`)).solutions,
  getSolution: (puzzleId: string, name: string) =>
    request<{ name: string; yaml: string }>(`/api/puzzles/${enc(puzzleId)}/solutions/${enc(name)}`),
  saveSolution: (puzzleId: string, name: string, yaml: string) =>
    request<{ name: string; saved: boolean }>(`/api/puzzles/${enc(puzzleId)}/solutions/${enc(name)}`, {
      method: 'PUT',
      body: JSON.stringify({ yaml }),
    }),
  getScores: (puzzleId: string) => request<ScoreRecord>(`/api/puzzles/${enc(puzzleId)}/scores`),
  /** The server runs every test itself and records the score if all pass. */
  submitScore: (puzzleId: string, yaml: string) =>
    request<ScoreResponse>(`/api/puzzles/${enc(puzzleId)}/scores`, { method: 'POST', body: JSON.stringify({ yaml }) }),
  getBestSolution: (puzzleId: string, dimension: ScoreDimension) =>
    request<{ name: string; yaml: string }>(`/api/puzzles/${enc(puzzleId)}/scores/${enc(dimension)}/solution`),
}

/** Solution names accepted by the server. */
export const SOLUTION_NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/

export function isValidSolutionName(name: string): boolean {
  return SOLUTION_NAME_RE.test(name) && !name.includes('..')
}
