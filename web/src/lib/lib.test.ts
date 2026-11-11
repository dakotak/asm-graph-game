import { describe, expect, it } from 'vitest'
import type { CellSnapshot, Puzzle, SimEvent, Snapshot } from '../api/types'
import { checkConnection, connectionId, findConnection } from './connections'
import { catalog } from '../test/fakeBackend'
import { defaultFunctions, estimateCost, nextNodeId, portDirection, portPlacement, resolveCells } from './hardware'
import { formatEvent, RingBuffer } from './eventLog'
import { autoLayout } from './layout'
import { applyEvents, emptyView, viewFromSnapshot } from './simReducer'
import { adaptToPuzzle, emptySolution, parseSolution, serializeSolution, SolutionParseError } from './solution'
import { definedLabels, lineContext } from '../editor/context'

const puzzle: Puzzle = {
  schema_version: 1,
  id: 'double-input',
  cells: [
    { id: 'input', type: 'input', ports: ['P0'], config: { stream: 'numbers' } },
    { id: 'processor', type: 'standard', program_memory: 32 },
    { id: 'output', type: 'output', ports: ['P0'], config: { stream: 'result' } },
  ],
  tests: [{ name: 't', inputs: { numbers: [1] }, outputs: { result: [2] } }],
}
const programmable = (t: string) => t === 'standard'

describe('solution serialization', () => {
  it('round-trips connections, programs and layout', () => {
    const s = emptySolution(puzzle, programmable)
    s.connections.push({ from: { cell: 'input', port: 'P0' }, to: { cell: 'processor', port: 'P0' } })
    s.programs.processor = 'MOV P0, ACC\nADD ACC\nMOV ACC, P1'
    s.layout = { input: { x: 0.4, y: 10 }, processor: { x: 300, y: 10 } }
    s.extraMetadata = { notes: 'keep me' }
    const yaml = serializeSolution(s)
    expect(yaml).toContain('schema_version: 2')
    expect(yaml).toContain('processor: |\n') // block literal, as hand-written files use
    expect(yaml).not.toContain('cells:')
    const back = parseSolution(yaml)
    expect(back.puzzle).toBe('double-input')
    expect(back.connections).toEqual(s.connections)
    expect(back.programs.processor).toBe('MOV P0, ACC\nADD ACC\nMOV ACC, P1\n')
    expect(back.layout.input).toEqual({ x: 0, y: 10 })
    expect(back.extraMetadata).toEqual({ notes: 'keep me' })
  })

  it('omits empty programs and empty metadata', () => {
    const yaml = serializeSolution(emptySolution(puzzle, programmable))
    expect(yaml).not.toContain('metadata')
    expect(parseSolution(yaml).programs).toEqual({})
  })

  it('parses the repository example solution format', () => {
    const s = parseSolution(`schema_version: 1
puzzle: double-input
connections:
  - from: {cell: input, port: p0}
    to: {cell: processor, port: P0}
programs:
  processor: |
    MOV P0, ACC
metadata:
  layout:
    input: {x: 0, y: 0}
    bogus: nonsense
`)
    expect(s.connections[0].from.port).toBe('P0')
    expect(s.layout).toEqual({ input: { x: 0, y: 0 } })
  })

  it('rejects files that redefine puzzle hardware or are malformed', () => {
    expect(() => parseSolution('schema_version: 1\npuzzle: x\ncells: []\n')).toThrow(/cannot define "cells"/)
    expect(() => parseSolution('schema_version: 3\npuzzle: x\n')).toThrow(SolutionParseError)
    expect(() => parseSolution('puzzle: [unclosed')).toThrow(/Not valid YAML/)
    expect(() => parseSolution('schema_version: 1\npuzzle: x\nconnections: [{from: 1}]\n')).toThrow(/connections\[0\]/)
    expect(() => parseSolution('- just a list')).toThrow(/mapping/)
  })

  it('adapts imported solutions to the puzzle', () => {
    const s = parseSolution('schema_version: 1\npuzzle: double-input\nprograms:\n  ghost: NOP\n')
    const { solution, warnings } = adaptToPuzzle(s, puzzle, programmable)
    expect(solution.programs).toEqual({ processor: '' })
    expect(warnings[0]).toMatch(/ghost/)
  })
})

describe('connection checks', () => {
  const cells = resolveCells(puzzle, null, catalog, null, null)
  const conns = [{ from: { cell: 'input', port: 'P0' }, to: { cell: 'processor', port: 'P0' } }]
  it('accepts free, enabled ports', () => {
    expect(checkConnection(cells, conns, { cell: 'processor', port: 'P1' }, { cell: 'output', port: 'P0' })).toEqual({ ok: true })
  })
  it.each([
    [{ cell: 'processor', port: 'P1' }, { cell: 'output', port: 'P2' }, /disabled/],
    [{ cell: 'processor', port: 'P0' }, { cell: 'output', port: 'P0' }, /already connected/],
    [{ cell: 'processor', port: 'P1' }, { cell: 'processor', port: 'P2' }, /itself/],
    [{ cell: 'processor', port: 'ANY' }, { cell: 'output', port: 'P0' }, /not a physical port/],
    [{ cell: 'ghost', port: 'P0' }, { cell: 'output', port: 'P0' }, /Unknown cell/],
  ])('rejects %o -> %o', (a, b, re) => {
    const r = checkConnection(cells, conns, a, b)
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.reason).toMatch(re)
  })
  it('identifies connections independent of direction', () => {
    const c = conns[0]
    expect(connectionId(c)).toBe(connectionId({ from: c.to, to: c.from }))
    expect(findConnection(conns, c.to, c.from)).toBe(0)
  })
})

function cell(id: string, extra: Partial<CellSnapshot> = {}): CellSnapshot {
  return { id, type: 'standard', category: 'processor', programmable: true, pc: 0, address: 0, stage: 'ready', blocked: false, ports: [], state: { ACC: 0 }, instructionsCompleted: 0, ...extra }
}

const snapshot: Snapshot = {
  puzzle: 'p',
  test: 't',
  testIndex: 0,
  tick: 0,
  status: 'ready',
  cells: [cell('cpu'), { ...cell('input'), type: 'input', programmable: false, state: { position: 0 } }],
  connections: [],
  inputs: [{ cell: 'input', stream: 'xs', position: 0, length: 3 }],
  outputs: [{ cell: 'out', stream: 'ys', state: 'pending', expected: [2], received: [] }],
  metrics: { ticks: 0, instructions: 0, transfers: 0, programBytes: {}, totalProgramBytes: 0, cellsSupplied: 2, cellsUsed: 0, connections: 0, hardwareCost: 0 },
}

describe('event reducer', () => {
  it('applies committed events incrementally', () => {
    const v0 = viewFromSnapshot(snapshot)
    const events: SimEvent[] = [
      { seq: 1, tick: 1, type: 'port.transfer', cell: 'input', data: { from: { cell: 'input', port: 'P0' }, to: { cell: 'cpu', port: 'P0' }, value: 7 } },
      { seq: 2, tick: 1, type: 'cell.state', cell: 'cpu', data: cell('cpu', { pc: 1, state: { ACC: 7 } }) },
      { seq: 3, tick: 1, type: 'cell.state', cell: 'input', data: { ...cell('input'), state: { position: 1 } } },
      { seq: 4, tick: 1, type: 'tick.completed', data: { status: 'running', instructions: 1, transfers: 1, progress: true } },
    ]
    const r = applyEvents(v0, 0, events)
    expect(r.view.cells.cpu.state.ACC).toBe(7)
    expect(r.view.inputs.input.position).toBe(1)
    expect(r.view.tick).toBe(1)
    expect(r.view.status).toBe('running')
    expect(r.transfers).toHaveLength(1)
    expect(r.transfers[0].value).toBe(7)
    expect(r.resync).toBe(false)
    expect(r.lastSeq).toBe(4)
    // The previous view is not mutated.
    expect(v0.cells.cpu.state.ACC).toBe(0)
    expect(v0.inputs.input.position).toBe(0)
  })

  it('detects missed events by seq gaps', () => {
    const r = applyEvents(emptyView, 10, [{ seq: 12, tick: 3, type: 'tick.completed', data: { status: 'running' } }])
    expect(r.resync).toBe(true)
  })

  it('records completion results and output validation', () => {
    const v0 = viewFromSnapshot(snapshot)
    const result = {
      test: 't',
      index: 0,
      status: 'passed',
      passed: true,
      metrics: { ...snapshot.metrics, ticks: 4 },
      outputs: [{ cell: 'out', stream: 'ys', state: 'passed', expected: [2], received: [2] }],
    }
    const r = applyEvents(v0, 0, [
      { seq: 1, tick: 4, type: 'output.validation', cell: 'out', data: result.outputs[0] },
      { seq: 2, tick: 4, type: 'simulation.completed', data: result },
    ])
    expect(r.terminal).toBe(true)
    expect(r.view.status).toBe('passed')
    expect(r.view.outputs.out.received).toEqual([2])
    expect(r.view.result?.metrics.ticks).toBe(4)
  })

  it('tolerates unknown event types', () => {
    const r = applyEvents(emptyView, 0, [{ seq: 1, tick: 0, type: 'future.event' as never }])
    expect(r.view).toBe(emptyView)
  })
})

describe('event log', () => {
  it('is bounded', () => {
    const rb = new RingBuffer<number>(3)
    for (let i = 0; i < 10; i++) rb.push(i)
    expect(rb.toArray()).toEqual([7, 8, 9])
    expect(rb.size).toBe(3)
    rb.clear()
    expect(rb.toArray()).toEqual([])
  })

  it('formats events for humans with the cells involved', () => {
    const t = formatEvent({ seq: 1, tick: 12, type: 'port.transfer', data: { from: { cell: 'a', port: 'P1' }, to: { cell: 'b', port: 'P0' }, value: 42 } })
    expect(t?.text).toBe('a.P1 → b.P0  42')
    expect(t?.cells).toEqual(['a', 'b'])
    const b = formatEvent({ seq: 2, tick: 13, type: 'cell.blocked', cell: 'a', data: { reason: 'waiting to write to P1' } })
    expect(b?.level).toBe('warn')
    expect(formatEvent({ seq: 3, tick: 13, type: 'tick.completed', data: {} })).toBeNull()
  })
})

describe('auto layout', () => {
  it('lays out cyclic graphs and separates I/O columns', () => {
    const conns = [
      { from: { cell: 'input', port: 'P0' }, to: { cell: 'processor', port: 'P0' } },
      { from: { cell: 'processor', port: 'P1' }, to: { cell: 'output', port: 'P0' } },
    ]
    const isIn = (c: { id: string }) => c.id === 'input'
    const isOut = (c: { id: string }) => c.id === 'output'
    const pos = autoLayout(puzzle.cells, conns, isIn, isOut)
    expect(pos.input.x).toBeLessThan(pos.processor.x)
    expect(pos.processor.x).toBeLessThan(pos.output.x)
    // No edges: three columns.
    const loose = autoLayout(puzzle.cells, [], isIn, isOut)
    expect(new Set(Object.values(loose).map((p) => p.x)).size).toBe(3)
    // Cycles are fine.
    const cyc = autoLayout(puzzle.cells, [...conns, { from: { cell: 'output', port: 'P1' }, to: { cell: 'input', port: 'P1' } }], isIn, isOut)
    expect(Object.keys(cyc)).toHaveLength(3)
  })
})

describe('editor context', () => {
  const c = ['#', ';']
  it('finds the mnemonic and operand index at the cursor', () => {
    expect(lineContext('', 1, c)).toMatchObject({ mnemonic: null })
    expect(lineContext('LOOP: ', 7, c)).toMatchObject({ mnemonic: null })
    expect(lineContext('MOV ', 5, c)).toMatchObject({ mnemonic: 'MOV', operandIndex: 0 })
    expect(lineContext('  mov P0, ', 11, c)).toMatchObject({ mnemonic: 'MOV', operandIndex: 1 })
    expect(lineContext('L: JMP ', 8, c)).toMatchObject({ mnemonic: 'JMP', operandIndex: 0 })
    expect(lineContext('MOV P0 # comment', 16, c)).toMatchObject({ inComment: true })
  })
  it('collects defined labels', () => {
    expect(definedLabels('A: B: NOP\n  LOOP: MOV P0, ACC # X: not a label\nJMP A', c).sort()).toEqual(['A', 'B', 'LOOP'])
  })
})

describe('hardware', () => {
  const v2: Puzzle = {
    schema_version: 2,
    id: 'hw',
    cells: [
      { id: 'in', type: 'input', config: { stream: 'xs' } },
      { id: 'out', type: 'output', config: { stream: 'ys' } },
    ],
    hardware: { max_cost: 50 },
    tests: [{ name: 't', inputs: { xs: [1] }, outputs: { ys: [1] } }],
  }
  const stackSpec = catalog.types.find((t) => t.type === 'stack')!.hardware
  const eff = {
    type: 'stack',
    spec: stackSpec,
    ports: { min: 2, max: 6, default: 4 },
    functions: ['PUSH', 'POP', 'SIZE'],
    functionMax: { PUSH: 1 },
    cost: stackSpec.cost,
    min: 0,
    max: 0,
  }

  it('resolves fixed cells and player nodes with their defaults', () => {
    const sol = {
      ...emptySolution(v2, () => false),
      hardware: [
        { id: 'cpu1', type: 'standard', port_count: 6 },
        { id: 'st', type: 'stack', port_count: 5 },
      ],
    }
    const cells = resolveCells(v2, sol, catalog, [eff], null)
    expect(cells.map((c) => [c.id, c.player, c.portCount])).toEqual([
      ['in', false, 1],
      ['out', false, 1],
      ['cpu1', true, 6],
      ['st', true, 5],
    ])
    expect(cells[2].programMemory).toBe(32)
    // The puzzle forbids PEEK and allows one PUSH, like the Go default.
    expect(cells[3].portFunctions).toEqual(['PUSH', 'POP', 'POP', 'SIZE', 'POP'])
    // v1 cells always have four ports.
    expect(resolveCells(puzzle, null, catalog, null, null)[0].portCount).toBe(4)
  })

  it('mirrors the backend cost model and default assignment', () => {
    expect(estimateCost({ base: 10, perPort: 2, properties: [{ property: 'program_memory', cost: 1, per: 8 }] }, { ports: 6, programMemory: 9 })).toBe(10 + 12 + 2)
    expect(defaultFunctions(stackSpec, undefined, 6)).toEqual(['PUSH', 'POP', 'PEEK', 'SIZE', 'PUSH', 'POP'])
    expect(nextNodeId('standard', new Set(['cpu1']))).toBe('cpu2')
  })

  it('spreads ports around the node', () => {
    const p = portPlacement(6)
    expect(p.map((x) => x.side)).toEqual(['left', 'right', 'top', 'bottom', 'left', 'right'])
    expect(p[0].offset).toBeCloseTo(100 / 3)
    expect(p[4].offset).toBeCloseTo(200 / 3)
    expect(p[2].offset).toBe(50)
  })

  it('refuses connections between ports that can never exchange data', () => {
    const sol = { ...emptySolution(v2, () => false), hardware: [{ id: 'st', type: 'stack', port_count: 4 }] }
    const cells = resolveCells(v2, sol, catalog, [eff], null)
    const roles: Record<string, 'input' | 'output'> = { in: 'input', out: 'output' }
    const dir = (c: (typeof cells)[number], p: string) => portDirection(c, p, catalog, roles[c.id])
    // input -> PUSH is fine; input -> POP (both only send) is not.
    expect(checkConnection(cells, [], { cell: 'in', port: 'P0' }, { cell: 'st', port: 'P0' }, dir).ok).toBe(true)
    const r = checkConnection(cells, [], { cell: 'in', port: 'P0' }, { cell: 'st', port: 'P1' }, dir)
    expect(r.ok).toBe(false)
    if (!r.ok) expect(r.reason).toMatch(/only send values/)
    const missing = checkConnection(cells, [], { cell: 'in', port: 'P0' }, { cell: 'st', port: 'P7' }, dir)
    expect(!missing.ok && missing.reason).toMatch(/has no port P7/)
  })

  it('round-trips player hardware through YAML', () => {
    const sol = {
      ...emptySolution(v2, () => false),
      hardware: [{ id: 'st', type: 'stack', port_count: 2, port_functions: ['PUSH', 'POP'], config: { capacity: 9 } }],
    }
    const yaml = serializeSolution(sol)
    expect(yaml).toContain('port_functions:')
    expect(parseSolution(yaml).hardware).toEqual(sol.hardware)
    // A fixed-topology puzzle drops player hardware (with a warning).
    const { solution, warnings } = adaptToPuzzle(parseSolution(yaml.replace('puzzle: hw', 'puzzle: double-input')), puzzle, (t) => t === 'standard')
    expect(solution.hardware).toEqual([])
    expect(warnings[0]).toMatch(/fixed topology/)
  })
})
