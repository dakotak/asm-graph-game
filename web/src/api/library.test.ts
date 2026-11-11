import { describe, expect, it } from 'vitest'
import { BrowserLibrary, better, type EngineCall, type KeyValue } from './library'
import { RpcError } from './rpc'
import { puzzle } from '../test/fakeBackend'

function memory(): KeyValue & { data: Map<string, string> } {
  const data = new Map<string, string>()
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) }
}

/** A fake engine: summaries by the YAML's "id:" line, scripted evaluations. */
function engine(score: () => unknown): EngineCall {
  return (async (method: string, params: { yaml?: string; solution?: string }) => {
    if (method === 'summarizePuzzle') {
      const id = /id: (\S+)/.exec(params.yaml ?? '')?.[1]
      if (!id) return { summary: { id: 'broken', valid: false, diagnostics: [{ severity: 'error', message: 'no id' }] }, puzzle: null }
      return { summary: { id, name: id, valid: true }, puzzle: { ...puzzle, id } }
    }
    if (method === 'evaluateSolution') {
      if (params.solution?.includes('INVALID')) throw new RpcError(-32001, 'solution is invalid', { diagnostics: [{ severity: 'error', message: 'bad port' }] })
      return score()
    }
    throw new Error(method)
  }) as EngineCall
}

const builtin = [{ file: 'double-input.yaml', yaml: 'id: double-input' }]
const passing = (s: { cost: number; ticks: number; bytes: number }) => () => ({ passed: true, score: s, tests: [], summary: {} })

describe('BrowserLibrary', () => {
  it('lists bundled puzzles and stores solutions in the browser', async () => {
    const store = memory()
    const lib = new BrowserLibrary(engine(passing({ cost: 1, ticks: 1, bytes: 1 })), store, builtin)
    expect((await lib.listPuzzles()).map((p) => [p.id, p.solutions])).toEqual([['double-input', 0]])
    await lib.saveSolution('double-input', 'main', 'schema_version: 2\npuzzle: double-input\n')
    expect((await lib.listPuzzles())[0].solutions).toBe(1)
    expect((await lib.getSolution('double-input', 'main')).yaml).toContain('puzzle: double-input')
    await expect(lib.saveSolution('double-input', 'x', 'schema_version: 2\npuzzle: other\n')).rejects.toThrow(/for puzzle "other"/)
    await expect(lib.saveSolution('double-input', '../evil', 'schema_version: 2\npuzzle: double-input\n')).rejects.toThrow(/Invalid solution name/)
    await lib.deleteSolution('double-input', 'main')
    expect(await lib.listSolutions('double-input')).toEqual([])
  })

  it('imports puzzles, refusing invalid files and built-in ids', async () => {
    const store = memory()
    const lib = new BrowserLibrary(engine(passing({ cost: 1, ticks: 1, bytes: 1 })), store, builtin)
    await lib.importPuzzle('mine.yaml', 'id: mine')
    expect((await lib.listPuzzles()).map((p) => [p.id, p.imported])).toEqual([
      ['double-input', false],
      ['mine', true],
    ])
    expect((await lib.getPuzzle('mine')).yaml).toBe('id: mine')
    await expect(lib.importPuzzle('x.yaml', 'id: double-input')).rejects.toThrow(/built-in puzzle already uses/)
    await expect(lib.importPuzzle('x.yaml', 'nonsense')).rejects.toThrow(/not a valid puzzle/)
    await lib.removePuzzle('mine')
    expect((await lib.listPuzzles()).map((p) => p.id)).toEqual(['double-input'])
  })

  it('keeps an independent personal best per dimension, scored by the engine', async () => {
    let next = { cost: 20, ticks: 40, bytes: 12 }
    const lib = new BrowserLibrary(engine(() => passing(next)()), memory(), builtin)
    const sol = (tag: string) => `schema_version: 2\npuzzle: double-input\n# ${tag}\n`
    expect((await lib.submitScore('double-input', sol('cheap'))).improved).toEqual(['cost', 'ticks', 'bytes'])
    next = { cost: 54, ticks: 28, bytes: 24 }
    expect((await lib.submitScore('double-input', sol('fast'))).improved).toEqual(['ticks'])
    expect((await lib.submitScore('double-input', sol('again'))).improved).toEqual([])
    expect((await lib.getBestSolution('double-input', 'ticks')).yaml).toContain('fast')
    expect((await lib.getBestSolution('double-input', 'cost')).yaml).toContain('cheap')
    expect((await lib.getScores('double-input')).bests.cost?.score.cost).toBe(20)
    await expect(lib.submitScore('double-input', 'INVALID')).rejects.toMatchObject({ status: 422, diagnostics: [{ message: 'bad port' }] })
  })

  it('compares scores like the Go scoring package', () => {
    const a = { cost: 20, ticks: 40, bytes: 12 }
    expect(better(a, { ...a, cost: 21 }, 'cost')).toBe(true)
    expect(better({ ...a, ticks: 39 }, a, 'cost')).toBe(true) // tie broken by ticks
    expect(better(a, a, 'bytes')).toBe(false)
  })
})
