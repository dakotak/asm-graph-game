// @vitest-environment node
//
// Runs the real engine — the Go code compiled to WebAssembly, exactly what
// the browser runs — in Node, and plays the example puzzles through the
// same JSON-RPC calls the IDE makes. `npm test` builds the engine first.
import { readFile } from 'node:fs/promises'
import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { beforeAll, describe, expect, it } from 'vitest'
import { loadEngine, type WasmEngine } from './host'

const dir = fileURLToPath(new URL('../../public/engine/', import.meta.url))
const examples = fileURLToPath(new URL('../../../examples/', import.meta.url))
const built = existsSync(dir + 'asmgraph.wasm')

let engine: WasmEngine
let id = 0
const events: { method: string; params: { type: string; data?: { status?: string } } }[] = []

function call<T = Record<string, unknown>>(method: string, params?: unknown): T {
  const resp = JSON.parse(engine.handle(JSON.stringify({ jsonrpc: '2.0', id: ++id, method, params }))!)
  if (resp.error) throw new Error(`${method}: ${resp.error.message} ${JSON.stringify(resp.error.data ?? '')}`)
  return resp.result as T
}

const read = (p: string) => readFile(examples + p, 'utf8')

describe.skipIf(!built)('WebAssembly engine', () => {
  beforeAll(async () => {
    engine = await loadEngine({ loadRuntime: () => import(/* @vite-ignore */ dir + 'wasm_exec.js'), wasm: await readFile(dir + 'asmgraph.wasm') })
    engine.onEvent((n) => events.push(JSON.parse(n)))
  })

  it('speaks protocol version 2 and knows every built-in cell type', () => {
    const info = call<{ protocolVersion: string; cellTypes: string[] }>('initialize')
    expect(info.protocolVersion).toBe('2')
    expect(info.cellTypes).toEqual(['input', 'output', 'stack', 'stack_processor', 'standard'])
  })

  it('summarizes every bundled puzzle and scores every example solution', async () => {
    for (const [p, s, score] of [
      ['double-input', 'double-input', undefined],
      ['stack-reverse', 'stack-reverse', { cost: 32, ticks: 92, bytes: 26 }],
      ['polynomial-design', 'polynomial-design-cheap', { cost: 20, ticks: 40, bytes: 12 }],
      ['polynomial-design', 'polynomial-design-fast', { cost: 54, ticks: 28, bytes: 24 }],
    ] as const) {
      const puz = await read(`puzzles/${p}.yaml`)
      const sum = call<{ summary: { id: string; valid: boolean } }>('summarizePuzzle', { yaml: puz, file: `${p}.yaml` })
      expect(sum.summary).toMatchObject({ id: p, valid: true })
      const res = call<{ passed: boolean; score?: unknown }>('evaluateSolution', { puzzle: puz, solution: await read(`solutions/${s}.yaml`) })
      expect(res.passed).toBe(true)
      if (score) expect(res.score).toEqual(score)
    }
  })

  it('runs an interactive session with events, like the debugger', async () => {
    call('loadPuzzle', { yaml: await read('puzzles/stack-reverse.yaml') })
    call('loadSolution', { yaml: await read('solutions/stack-reverse.yaml') })
    call('setEventLevel', { level: 'ticks' })
    call('start', { test: 0 })
    call('step', { ticks: 5 })
    expect(call<{ tick: number }>('getState').tick).toBe(5)
    events.length = 0
    call('run', { delayMs: 0 })
    // Unpaced runs yield, so they finish in the background and report.
    for (let i = 0; i < 100 && !events.some((e) => e.params.type === 'simulation.completed'); i++) await new Promise((r) => setTimeout(r, 10))
    expect(call<{ status: string; tick: number }>('getState')).toMatchObject({ status: 'passed', tick: 52 })
    expect(events.some((e) => e.params.type === 'port.transfer')).toBe(true)
  })

  it('can pause a long unpaced run', async () => {
    call('loadPuzzle', { yaml: await read('puzzles/double-input.yaml') })
    // A processor that never outputs: the run only ends at the tick limit.
    call('loadSolution', { yaml: 'schema_version: 2\npuzzle: double-input\nconnections: []\nprograms:\n  processor: |\n    ADD 1\n' })
    call('start', { test: 0, maxTicks: 50_000_000 })
    call('run', { delayMs: 0 })
    await new Promise((r) => setTimeout(r, 50))
    const paused = call<{ status: string; tick: number }>('pause')
    expect(paused.status).toBe('running')
    expect(paused.tick).toBeGreaterThan(0)
  })
})
